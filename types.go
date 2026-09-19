// Package cpm is the critical path method scheduling kernel.
//
// The package is split by responsibility:
//   - types.go     : data structures exchanged with callers
//   - validate.go  : input validation (duplicate ids, unknown predecessors,
//     non-positive durations, reversed three-point estimates)
//   - topo.go      : topological sorting (directed cycle rejection)
//   - schedule.go  : forward pass (ES/EF) and backward pass (LS/LF), floats
//   - paths.go     : critical path enumeration and PERT variance/probability
//   - demo.go      : built-in civil engineering demonstration network
package cpm

import "time"

// ThreePoint is a PERT estimate: optimistic, most likely and pessimistic
// durations in days. All three must be positive and satisfy O <= M <= P.
type ThreePoint struct {
	Optimistic  float64 `json:"optimistic"`
	MostLikely  float64 `json:"most_likely"`
	Pessimistic float64 `json:"pessimistic"`
}

// ActivityInput is one submitted activity (process) of the network.
//
// Exactly one duration form must be given: either deterministic Duration > 0,
// or a PERT triple. Predecessors reference other activities by ID; activities
// without predecessors start from the virtual source node.
type ActivityInput struct {
	ID         string      `json:"id"`
	Duration   *float64    `json:"duration,omitempty"`
	ThreePoint *ThreePoint `json:"three_point,omitempty"`
	Pred       []string    `json:"predecessors,omitempty"`
}

// NetworkInput is a submitted activity-on-node network.
//
// Target is the target completion time measured in days from project start;
// when three-point durations and a target are given, the completion
// probability is estimated with a normal approximation.
type NetworkInput struct {
	Name       string          `json:"name"`
	Activities []ActivityInput `json:"activities"`
	Target     *float64        `json:"target,omitempty"`
}

// ErrorType enumerates the typed validation failures returned by the kernel.
// HTTP handlers map these kinds to status codes; clients can branch on the
// string instead of matching human readable messages.
type ErrorType string

const (
	ErrEmptyNetwork    ErrorType = "EMPTY_NETWORK"
	ErrMissingID       ErrorType = "MISSING_ID"
	ErrDuplicateID     ErrorType = "DUPLICATE_ID"
	ErrUnknownPred     ErrorType = "UNKNOWN_PREDECESSOR"
	ErrSelfPrecedence  ErrorType = "SELF_PRECEDENCE"
	ErrCycle           ErrorType = "DIRECTED_CYCLE"
	ErrBadDuration     ErrorType = "INVALID_DURATION"
	ErrBadThreePoint   ErrorType = "INVALID_THREE_POINT"
	ErrMissingEstimate ErrorType = "MISSING_ESTIMATE"
	ErrInvalidTarget   ErrorType = "INVALID_TARGET"
	ErrBrokenKernel    ErrorType = "KERNEL_INVARIANT_BROKEN"
)

// ValidationError carries a machine readable error kind plus details.
type ValidationError struct {
	Kind    ErrorType `json:"kind"`
	Message string    `json:"message"`
}

func (e *ValidationError) Error() string { return string(e.Kind) + ": " + e.Message }

// ActivityResult holds the four time parameters and floats of one activity.
type ActivityResult struct {
	ID         string   `json:"id"`
	Duration   float64  `json:"duration"`
	ES         float64  `json:"earliest_start"`
	EF         float64  `json:"earliest_finish"`
	LS         float64  `json:"latest_start"`
	LF         float64  `json:"latest_finish"`
	TotalFloat float64  `json:"total_float"`
	FreeFloat  float64  `json:"free_float"`
	Critical   bool     `json:"critical"`
	Pred       []string `json:"predecessors"`
	Succ       []string `json:"successors"`

	// PERT fields, present only when the activity gave a three-point estimate.
	ThreePoint *ThreePoint `json:"three_point,omitempty"`
	Variance   *float64    `json:"variance,omitempty"`
}

// PERTResult holds the probabilistic schedule produced from three-point
// durations and, optionally, a target completion date.
type PERTResult struct {
	MeanDuration float64 `json:"mean_duration"`
	Variance     float64 `json:"project_variance"`
	StdDev       float64 `json:"standard_deviation"`
	// SelectedPath is the critical path whose variances were summed: when
	// several paths are critical the one with the largest variance is used.
	SelectedPath []string `json:"selected_critical_path"`
	// SelectedVariancePerActivity records variance by activity on the
	// selected path, deterministic activities contributing 0.
	PathVariances    map[string]float64 `json:"path_variances"`
	AllPathVariances []PathVariance     `json:"all_critical_path_variances"`

	// Probability fields are empty when no target date was supplied.
	Target          *float64 `json:"target,omitempty"`
	Probability     *float64 `json:"completion_probability,omitempty"`
	ProbabilityText string   `json:"probability_formula"`
}

// PathVariance ties one critical path to the sum of its activity variances.
type PathVariance struct {
	Path     []string `json:"path"`
	Variance float64  `json:"variance"`
}

// Job is a persisted planning job: the submitted network, all computed
// schedule parameters, critical path(s) and optional PERT results.
type Job struct {
	Number          int              `json:"number"`
	Name            string           `json:"name"`
	SubmittedAt     time.Time        `json:"submitted_at"`
	Activities      []ActivityResult `json:"activities"`
	CriticalPaths   [][]string       `json:"critical_paths"`
	ProjectDuration float64          `json:"project_duration"`
	PERT            *PERTResult      `json:"pert,omitempty"`
	Source          string           `json:"source"`

	// Normalized inputs are kept so a fetched job shows predecessors and the
	// exact estimates used.
	Input NetworkInput `json:"input"`
}
