package cpm

import (
	"math"
)

// Validate checks a submitted network independently of any scheduling math.
// The whole network is rejected (the kernel never runs) when:
//   - the activity list is empty,
//   - an id is missing or duplicated,
//   - a predecessor is unknown or the activity points at itself,
//   - a duration is not a positive number,
//   - a three-point estimate is missing parts, non-positive, non-finite or
//     not ordered optimistic <= most likely <= pessimistic,
//   - both duration forms are given (or neither),
//   - the target date is not a finite number.
func Validate(in NetworkInput) error {
	if len(in.Activities) == 0 {
		return &ValidationError{Kind: ErrEmptyNetwork, Message: "network has no activities"}
	}

	seen := make(map[string]bool, len(in.Activities))
	for i := range in.Activities {
		a := &in.Activities[i]
		if a.ID == "" {
			return &ValidationError{Kind: ErrMissingID, Message: "activity at position has empty id"}
		}
		if seen[a.ID] {
			return &ValidationError{Kind: ErrDuplicateID, Message: "duplicate activity id: " + a.ID}
		}
		seen[a.ID] = true

		if (a.Duration == nil) == (a.ThreePoint == nil) {
			if a.Duration == nil {
				return &ValidationError{Kind: ErrMissingEstimate, Message: "activity " + a.ID + " must give either duration or three_point"}
			}
			return &ValidationError{Kind: ErrMissingEstimate, Message: "activity " + a.ID + " gives both duration and three_point, give exactly one"}
		}

		if a.Duration != nil {
			if !positiveFinite(*a.Duration) {
				return &ValidationError{Kind: ErrBadDuration, Message: "activity " + a.ID + " duration must be a positive finite number of days (zero-duration milestones are not activities)"}
			}
		} else {
			tp := a.ThreePoint
			if !positiveFinite(tp.Optimistic) || !positiveFinite(tp.MostLikely) || !positiveFinite(tp.Pessimistic) {
				return &ValidationError{Kind: ErrBadThreePoint, Message: "activity " + a.ID + " three-point durations must all be positive finite numbers"}
			}
			if tp.Optimistic > tp.MostLikely || tp.MostLikely > tp.Pessimistic {
				return &ValidationError{Kind: ErrBadThreePoint, Message: "activity " + a.ID + " three-point durations must satisfy optimistic <= most_likely <= pessimistic"}
			}
		}
	}

	// Predecessor references are checked only after every id is known, so the
	// error reported for a forward reference is "unknown" rather than an
	// artifact of list order.
	for i := range in.Activities {
		a := &in.Activities[i]
		for _, p := range a.Pred {
			if p == a.ID {
				return &ValidationError{Kind: ErrSelfPrecedence, Message: "activity " + a.ID + " lists itself as predecessor"}
			}
			if !seen[p] {
				return &ValidationError{Kind: ErrUnknownPred, Message: "activity " + a.ID + " has unknown predecessor: " + p}
			}
		}
	}

	if in.Target != nil && !isFinite(*in.Target) {
		return &ValidationError{Kind: ErrInvalidTarget, Message: "target date must be a finite number of days"}
	}
	return nil
}

func positiveFinite(x float64) bool {
	return x > 0 && isFinite(x)
}

func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// normalized returns a copy of the activities with predecessor lists
// de-duplicated while preserving first-occurrence order.
func normalized(in NetworkInput) []ActivityInput {
	out := make([]ActivityInput, len(in.Activities))
	copy(out, in.Activities)
	for i := range out {
		out[i].Pred = uniqueStrings(out[i].Pred)
	}
	return out
}

func uniqueStrings(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
