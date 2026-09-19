package cpm

import (
	"math"
	"sort"
	"strings"
)

// epsilon tolerates floating point round-off in float equality. Schedule
// numbers come from additions of decimal inputs, so strict equality is
// unreliable; anything below epsilon is treated as exactly zero.
const epsilon = 1e-9

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

func joinStrings(xs []string) string { return strings.Join(xs, ", ") }

// expectedDuration implements the PERT beta-PERT mean (O + 4M + P) / 6.
func expectedDuration(tp ThreePoint) float64 {
	return (tp.Optimistic + 4*tp.MostLikely + tp.Pessimistic) / 6
}

// varianceDuration implements the PERT variance ((P - O) / 6)^2.
func varianceDuration(tp ThreePoint) float64 {
	d := (tp.Pessimistic - tp.Optimistic) / 6
	return d * d
}

// nearZero reports whether a computed float is zero within round-off.
func nearZero(x float64) bool { return math.Abs(x) < epsilon }

// approxEqual reports float equality within the scheduling epsilon.
func approxEqual(a, b float64) bool { return math.Abs(a-b) < epsilon }

// cleanFloat turns -0 and round-off dust like 1e-15 into a clean 0.
func cleanFloat(x float64) float64 {
	if math.Abs(x) < epsilon {
		return 0
	}
	return x
}

// nonNilStrings guarantees an empty list serializes as [] rather than null.
func nonNilStrings(xs []string) []string {
	if len(xs) == 0 {
		return []string{}
	}
	return xs
}
