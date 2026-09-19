package cpm

import "math"

// scheduledActivity is the internal working representation: inputs plus the
// four time parameters and floats, indexed by id.
type scheduledActivity struct {
	in     ActivityInput
	dur    float64
	pred   []string
	succ   []string
	es, ef float64
	ls, lf float64
	tf, ff float64
}

// forwardBackward runs the CPM time analysis on a validated, acyclic network.
//
// Virtual source/sink nodes are implicit: activities without predecessors
// start at time 0 (from the virtual source); activities without successors
// finish into the virtual sink, whose earliest finish equals the project
// duration. The backward pass starts every sink activity at that duration.
func forwardBackward(acts []ActivityInput, order []string) (map[string]*scheduledActivity, float64, error) {
	m := make(map[string]*scheduledActivity, len(acts))
	for i := range acts {
		a := &acts[i]
		sa := &scheduledActivity{in: *a, pred: append([]string(nil), a.Pred...)}
		if a.Duration != nil {
			sa.dur = *a.Duration
		} else {
			sa.dur = expectedDuration(*a.ThreePoint)
		}
		m[a.ID] = sa
	}
	for id, sa := range m {
		for _, p := range sa.pred {
			m[p].succ = append(m[p].succ, id)
		}
	}

	// Forward pass: ES = max EF of predecessors; EF = ES + duration.
	// Parallel branches merge at the larger EF at their junction.
	for _, id := range order {
		sa := m[id]
		es := 0.0
		for _, p := range sa.pred {
			if m[p].ef > es {
				es = m[p].ef
			}
		}
		sa.es = es
		sa.ef = es + sa.dur
	}

	// Project duration = EF of the virtual sink = max EF over sink activities.
	duration := 0.0
	for _, sa := range m {
		if len(sa.succ) == 0 && sa.ef > duration {
			duration = sa.ef
		}
	}

	// Backward pass in reverse topological order:
	// LF = min LS of successors (sinks start from the virtual sink at project
	// duration); LS = LF - duration.
	for i := len(order) - 1; i >= 0; i-- {
		sa := m[order[i]]
		if len(sa.succ) == 0 {
			sa.lf = duration
		} else {
			lf := math.Inf(1)
			for _, s := range sa.succ {
				if m[s].ls < lf {
					lf = m[s].ls
				}
			}
			sa.lf = lf
		}
		sa.ls = sa.lf - sa.dur
	}

	// Total float must satisfy both identities simultaneously:
	//   TF = LS - ES = LF - EF.
	// They are mathematically identical once EF = ES + d and LF = LS + d hold;
	// the kernel asserts the identity rather than trusting it silently.
	for _, id := range order {
		sa := m[id]
		tfA := sa.ls - sa.es
		tfB := sa.lf - sa.ef
		if math.Abs(tfA-tfB) > 1e-7 {
			return nil, 0, &ValidationError{
				Kind:    ErrBrokenKernel,
				Message: "float identity broken for " + id + ": LS-ES != LF-EF",
			}
		}
		sa.tf = cleanFloat(tfA)
		if sa.tf < 0 {
			return nil, 0, &ValidationError{
				Kind:    ErrBrokenKernel,
				Message: "negative total float for " + id,
			}
		}

		// Free float: how long the activity can slip without delaying any
		// successor's earliest start. FF can never exceed TF.
		ff := 0.0
		if len(sa.succ) > 0 {
			ff = math.Inf(1)
			for _, s := range sa.succ {
				gap := m[s].es - sa.ef
				if gap < ff {
					ff = gap
				}
			}
		} else {
			ff = duration - sa.ef
		}
		sa.ff = cleanFloat(ff)
		if sa.ff < -epsilon || sa.ff > sa.tf+epsilon {
			return nil, 0, &ValidationError{
				Kind:    ErrBrokenKernel,
				Message: "free float outside [0, total float] for " + id,
			}
		}
		if sa.ff < 0 {
			sa.ff = 0
		}
	}

	return m, duration, nil
}
