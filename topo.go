package cpm

// TopoSort returns activity IDs in a topological order: every predecessor is
// listed before the activity itself. It implements Kahn's algorithm.
//
// ids must already have passed validation (known, unique). A directed cycle
// leaves nodes with a positive residual in-degree; the remaining set is
// reported inside the typed error.
func TopoSort(acts []ActivityInput) ([]string, error) {
	indeg := make(map[string]int, len(acts))
	succ := make(map[string][]string, len(acts))
	for i := range acts {
		id := acts[i].ID
		if _, ok := indeg[id]; !ok {
			indeg[id] = 0
		}
	}
	for i := range acts {
		id := acts[i].ID
		indeg[id] = len(acts[i].Pred)
		for _, p := range acts[i].Pred {
			succ[p] = append(succ[p], id)
		}
	}

	// Seed with all source activities (no predecessors).
	var queue []string
	for _, id := range sortedKeys(indeg) {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}

	order := make([]string, 0, len(acts))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, s := range succ[id] {
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
	}

	if len(order) != len(acts) {
		var cyclic []string
		for id, d := range indeg {
			if d > 0 {
				cyclic = append(cyclic, id)
			}
		}
		return nil, &ValidationError{
			Kind:    ErrCycle,
			Message: "network contains a directed cycle among activities: " + joinStrings(sortedStrings(cyclic)),
		}
	}
	return order, nil
}

// successorMap indexes predecessor lists forward.
func successorMap(acts []ActivityInput) map[string][]string {
	succ := make(map[string][]string, len(acts))
	for i := range acts {
		for _, p := range acts[i].Pred {
			succ[p] = append(succ[p], acts[i].ID)
		}
	}
	return succ
}
