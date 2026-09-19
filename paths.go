package cpm

import (
	"math"
	"sort"
)

// CriticalPaths enumerates every source-to-sink path whose activities all
// have total float 0. Virtual source/sink nodes are used only to close the
// network; the returned paths contain exclusively caller-submitted IDs.
//
// Construction: an edge u -> v belongs to a critical path exactly when both
// endpoints are critical and EF(u) == ES(v) (zero free float on that edge).
// All source->sink paths in that critical subgraph are returned.
func CriticalPaths(m map[string]*scheduledActivity, order []string, duration float64) ([][]string, error) {
	isCritical := func(sa *scheduledActivity) bool { return nearZero(sa.tf) }

	// Build the critical subgraph adjacency.
	critSucc := map[string][]string{}
	var critIDs []string
	for _, id := range order {
		if isCritical(m[id]) {
			critIDs = append(critIDs, id)
		}
	}
	critSet := map[string]bool{}
	for _, id := range critIDs {
		critSet[id] = true
	}
	for _, id := range critIDs {
		sa := m[id]
		var cs []string
		for _, s := range sa.succ {
			// successor critical and this edge consumes zero slack
			if critSet[s] && approxEqual(sa.ef, m[s].es) {
				cs = append(cs, s)
			}
		}
		sort.Strings(cs)
		critSucc[id] = cs
	}

	// Defensive invariant: every critical activity must be reachable from a
	// critical source activity and reach a critical sink activity, i.e. the
	// critical nodes must string together into complete source-sink paths.
	var sources, sinks []string
	for _, id := range critIDs {
		sa := m[id]
		if len(sa.pred) == 0 && approxEqual(sa.es, 0) {
			sources = append(sources, id)
		}
		if len(sa.succ) == 0 && approxEqual(sa.ef, duration) {
			sinks = append(sinks, id)
		}
	}
	if len(sources) == 0 || len(sinks) == 0 {
		return nil, &ValidationError{
			Kind:    ErrBrokenKernel,
			Message: "critical activities do not form a source-to-sink path",
		}
	}
	reachableFromSource := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if reachableFromSource[id] {
			return
		}
		reachableFromSource[id] = true
		for _, s := range critSucc[id] {
			walk(s)
		}
	}
	for _, s := range sources {
		walk(s)
	}
	for _, id := range critIDs {
		if !reachableFromSource[id] {
			return nil, &ValidationError{
				Kind:    ErrBrokenKernel,
				Message: "critical activity not reachable from virtual source: " + id,
			}
		}
	}

	// Symmetric check: every critical activity must reach a critical sink.
	critPred := map[string][]string{}
	for id, cs := range critSucc {
		for _, s := range cs {
			critPred[s] = append(critPred[s], id)
		}
	}
	canReachSink := map[string]bool{}
	var walkUp func(string)
	walkUp = func(id string) {
		if canReachSink[id] {
			return
		}
		canReachSink[id] = true
		for _, p := range critPred[id] {
			walkUp(p)
		}
	}
	for _, s := range sinks {
		walkUp(s)
	}
	for _, id := range critIDs {
		if !canReachSink[id] {
			return nil, &ValidationError{
				Kind:    ErrBrokenKernel,
				Message: "critical activity cannot reach virtual sink: " + id,
			}
		}
	}

	// Enumerate all simple source->sink paths in the critical subgraph with
	// memoized DFS. The subgraph is a DAG (subset of the activity DAG).
	var all [][]string
	var dfs func(id string, prefix []string)
	dfs = func(id string, prefix []string) {
		prefix = append(prefix, id)
		if len(m[id].succ) == 0 {
			path := append([]string(nil), prefix...)
			all = append(all, path)
			return
		}
		for _, s := range critSucc[id] {
			dfs(s, prefix)
		}
	}
	for _, s := range sources {
		dfs(s, nil)
	}
	if len(all) == 0 {
		return nil, &ValidationError{
			Kind:    ErrBrokenKernel,
			Message: "could not enumerate a critical path spanning source to sink",
		}
	}
	return all, nil
}

// pertVariance sums activity variances along a critical path. Activities with
// deterministic durations contribute 0 variance (no fabricated variance).
func pertVariance(m map[string]*scheduledActivity, path []string) (float64, map[string]float64) {
	by := make(map[string]float64, len(path))
	total := 0.0
	for _, id := range path {
		var v float64
		if m[id].in.ThreePoint != nil {
			v = varianceDuration(*m[id].in.ThreePoint)
		}
		by[id] = v
		total += v
	}
	return total, by
}

// selectPERTPath implements the PERT rule: project variance is the sum along
// a chosen critical path; when several paths are critical the one with the
// largest variance is chosen. Ties (equal variance) break on the
// lexicographically smallest path so the choice is deterministic.
func selectPERTPath(m map[string]*scheduledActivity, paths [][]string) ([]string, float64, map[string]float64, []PathVariance) {
	all := make([]PathVariance, 0, len(paths))
	bestIdx := -1
	bestVar := -1.0
	for i, p := range paths {
		v, _ := pertVariance(m, p)
		all = append(all, PathVariance{Path: append([]string(nil), p...), Variance: cleanFloat(v)})
		if v > bestVar+epsilon {
			bestVar = v
			bestIdx = i
		} else if approxEqual(v, bestVar) && bestIdx >= 0 && lexLess(p, paths[bestIdx]) {
			bestIdx = i
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !approxEqual(all[i].Variance, all[j].Variance) {
			return all[i].Variance > all[j].Variance
		}
		return lexLess(all[i].Path, all[j].Path)
	})
	v, by := pertVariance(m, paths[bestIdx])
	return paths[bestIdx], cleanFloat(v), by, all
}

func lexLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// normalCDF evaluates the standard normal cumulative distribution function
// via the erf relation: Phi(z) = (1 + erf(z / sqrt(2))) / 2.
func normalCDF(z float64) float64 {
	return 0.5 * (1 + math.Erf(z/math.Sqrt2))
}

// probabilityFormula documents, in the result itself, the approximation used.
const probabilityFormula = "P(T <= target) = Phi((target - mean) / stddev), " +
	"Phi(z) = 0.5*(1+erf(z/sqrt(2))); " +
	"PERT mean = (O+4M+P)/6 per activity summed over the critical path, " +
	"variance = ((P-O)/6)^2 summed over the selected critical path. " +
	"When stddev is 0, P = 1 if target >= mean else 0."

// Calculate runs the complete CPM/PERT kernel on a submitted network.
// The job number is left zero; the file repository assigns it on save.
func Calculate(in NetworkInput) (*Job, error) {
	if err := Validate(in); err != nil {
		return nil, err
	}
	acts := normalized(in)

	order, err := TopoSort(acts)
	if err != nil {
		return nil, err
	}

	m, duration, err := forwardBackward(acts, order)
	if err != nil {
		return nil, err
	}

	crit, err := CriticalPaths(m, order, duration)
	if err != nil {
		return nil, err
	}

	results := make([]ActivityResult, 0, len(acts))
	hasThreePoint := false
	for _, id := range order {
		sa := m[id]
		cr := ActivityResult{
			ID:         id,
			Duration:   cleanFloat(sa.dur),
			ES:         cleanFloat(sa.es),
			EF:         cleanFloat(sa.ef),
			LS:         cleanFloat(sa.ls),
			LF:         cleanFloat(sa.lf),
			TotalFloat: cleanFloat(sa.tf),
			FreeFloat:  cleanFloat(sa.ff),
			Critical:   nearZero(sa.tf),
			Pred:       nonNilStrings(sa.pred),
			Succ:       nonNilStrings(sa.succ),
		}
		if sa.in.ThreePoint != nil {
			hasThreePoint = true
			tp := *sa.in.ThreePoint
			v := varianceDuration(tp)
			cr.ThreePoint = &tp
			cr.Variance = &v
		}
		results = append(results, cr)
	}

	job := &Job{
		Name:            in.Name,
		Activities:      results,
		CriticalPaths:   crit,
		ProjectDuration: cleanFloat(duration),
		Input:           in,
	}

	if hasThreePoint {
		selected, variance, by, allVars := selectPERTPath(m, crit)
		pr := &PERTResult{
			MeanDuration:     cleanFloat(duration),
			Variance:         variance,
			StdDev:           cleanFloat(math.Sqrt(math.Max(variance, 0))),
			SelectedPath:     selected,
			PathVariances:    by,
			AllPathVariances: allVars,
			ProbabilityText:  probabilityFormula,
		}
		if in.Target != nil {
			t := *in.Target
			var p float64
			if pr.StdDev <= epsilon {
				if t >= duration-epsilon {
					p = 1
				} else {
					p = 0
				}
			} else {
				z := (t - duration) / pr.StdDev
				p = normalCDF(z)
			}
			pr.Target = &t
			pr.Probability = &p
		}
		job.PERT = pr
	}

	return job, nil
}
