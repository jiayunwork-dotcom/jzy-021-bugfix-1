package cpm

import (
	"math"
	"strings"
	"testing"
)

func fp(x float64) *float64 { return &x }

func tp(o, m, p float64) *ThreePoint { return &ThreePoint{o, m, p} }

func expectErrorKind(t *testing.T, err error, want ErrorType) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Kind != want {
		t.Fatalf("expected error kind %s, got %s (%s)", want, ve.Kind, ve.Message)
	}
}

// assertFloatIdentity: TF = LS-ES and TF = LF-EF both hold, and integer
// durations produce exact 0 / positive-integer floats.
func assertFloatIdentities(t *testing.T, job *Job) {
	t.Helper()
	byID := map[string]ActivityResult{}
	for _, a := range job.Activities {
		byID[a.ID] = a
	}
	for _, a := range job.Activities {
		tf1 := a.LS - a.ES
		tf2 := a.LF - a.EF
		if math.Abs(tf1-tf2) > 1e-9 {
			t.Errorf("activity %s: LS-ES=%v but LF-EF=%v", a.ID, tf1, tf2)
		}
		if math.Abs(a.TotalFloat-tf1) > 1e-9 {
			t.Errorf("activity %s: stored total float %v != LS-ES %v", a.ID, a.TotalFloat, tf1)
		}
		if a.TotalFloat < -1e-9 {
			t.Errorf("activity %s: negative total float %v", a.ID, a.TotalFloat)
		}
		if a.FreeFloat > a.TotalFloat+1e-9 {
			t.Errorf("activity %s: free float %v > total float %v", a.ID, a.FreeFloat, a.TotalFloat)
		}
		if a.FreeFloat < -1e-9 {
			t.Errorf("activity %s: negative free float %v", a.ID, a.FreeFloat)
		}
	}
}

// assertPathSpansSourceToSink checks every critical path starts at an
// activity without predecessors (virtual source) and ends at one without
// successors (virtual sink), following real edges, and contains only
// caller-submitted ids (no synthetic nodes).
func assertPathSpansSourceToSink(t *testing.T, in NetworkInput, job *Job) {
	t.Helper()
	preds := map[string]map[string]bool{}
	succ := map[string]map[string]bool{}
	ids := map[string]bool{}
	for _, a := range in.Activities {
		ids[a.ID] = true
		preds[a.ID] = map[string]bool{}
		for _, p := range a.Pred {
			preds[a.ID][p] = true
		}
	}
	for id, ps := range preds {
		for p := range ps {
			if succ[p] == nil {
				succ[p] = map[string]bool{}
			}
			succ[p][id] = true
		}
	}
	if len(job.CriticalPaths) == 0 {
		t.Fatal("no critical paths produced")
	}
	for pi, path := range job.CriticalPaths {
		if len(path) == 0 {
			t.Fatalf("critical path %d is empty", pi)
		}
		for _, id := range path {
			if !ids[id] {
				t.Fatalf("critical path contains id not submitted: %q", id)
			}
			if strings.HasPrefix(id, "__") {
				t.Fatalf("virtual node leaked into critical path: %q", id)
			}
		}
		first, last := path[0], path[len(path)-1]
		if len(preds[first]) != 0 {
			t.Fatalf("path %v does not start at virtual source: %s has preds", path, first)
		}
		if len(succ[last]) != 0 {
			t.Fatalf("path %v does not end at virtual sink: %s has successors", path, last)
		}
		for i := 0; i+1 < len(path); i++ {
			if !succ[path[i]][path[i+1]] {
				t.Fatalf("path %v has no edge %s -> %s", path, path[i], path[i+1])
			}
		}
		for _, id := range path {
			var ar *ActivityResult
			for k := range job.Activities {
				if job.Activities[k].ID == id {
					ar = &job.Activities[k]
				}
			}
			if ar == nil || !ar.Critical {
				t.Fatalf("activity %s on critical path not flagged critical", id)
			}
		}
	}
}

func TestDemoNetworkHandCheck(t *testing.T) {
	in := DemoNetwork()
	job, err := Calculate(in)
	if err != nil {
		t.Fatalf("demo calculate: %v", err)
	}
	if job.ProjectDuration != 24 {
		t.Fatalf("project duration = %v, want 24", job.ProjectDuration)
	}
	if len(job.CriticalPaths) != 1 {
		t.Fatalf("want exactly 1 critical path, got %v", job.CriticalPaths)
	}
	want := []string{"A", "B", "C2", "D", "E"}
	if strings.Join(job.CriticalPaths[0], ",") != strings.Join(want, ",") {
		t.Fatalf("critical path = %v, want %v", job.CriticalPaths[0], want)
	}
	params := map[string][4]float64{} // ES EF LS LF
	for _, a := range job.Activities {
		params[a.ID] = [4]float64{a.ES, a.EF, a.LS, a.LF}
	}
	// Hand-computed table.
	expect := map[string][4]float64{
		"A":  {0, 3, 0, 3},
		"B":  {3, 8, 3, 8},
		"C1": {8, 12, 10, 14},
		"C2": {8, 14, 8, 14},
		"D":  {14, 22, 14, 22},
		"E":  {22, 24, 22, 24},
	}
	for id, w := range expect {
		if got := params[id]; got != w {
			t.Errorf("activity %s ES/EF/LS/LF = %v, want %v", id, got, w)
		}
	}
	for _, a := range job.Activities {
		// integer durations: total float is exactly 0 or a positive integer
		rf := a.TotalFloat - math.Floor(a.TotalFloat)
		if rf > 1e-9 {
			t.Errorf("activity %s total float %v not an integer", a.ID, a.TotalFloat)
		}
		if a.ID == "C1" {
			if a.TotalFloat != 2 || a.FreeFloat != 2 {
				t.Errorf("C1 floats = TF %v FF %v, want 2/2", a.TotalFloat, a.FreeFloat)
			}
			if a.Critical {
				t.Errorf("C1 must not be critical (parallel branch with positive float)")
			}
		}
	}
	assertFloatIdentities(t, job)
	assertPathSpansSourceToSink(t, in, job)

	// Deterministic-only network: no fabricated variance/probability.
	if job.PERT != nil {
		t.Fatalf("deterministic job must not carry PERT data: %+v", job.PERT)
	}
	for _, a := range job.Activities {
		if a.Variance != nil || a.ThreePoint != nil {
			t.Errorf("activity %s fabricated three-point data", a.ID)
		}
	}
}

// pertBranches builds a network with two parallel critical branches feeding a
// sink activity. Means are chosen equal by default so both branches are
// critical; widths differ to exercise path variance selection.
//
//	B (deterministic)
//
// S -> C (PERT) -> D
//
//	C is a two-branch structure S->{B,C}->D actually:
func pertNetwork(t *testing.T) NetworkInput {
	// Branch B: one PERT activity (mean 4), branch C: one PERT activity (mean 4),
	// then D deterministic 2.
	in := NetworkInput{
		Name: "pert-two-critical-branches",
		Activities: []ActivityInput{
			{ID: "S", Duration: fp(1)},
			{ID: "B", ThreePoint: tp(2, 4, 6), Pred: []string{"S"}}, // mean 4, var 4/9
			{ID: "C", ThreePoint: tp(1, 4, 7), Pred: []string{"S"}}, // mean 4, var 1
			{ID: "D", Duration: fp(2), Pred: []string{"B", "C"}},
		},
	}
	return in
}

func TestPERTTwoCriticalPathsAndVarianceSelection(t *testing.T) {
	in := pertNetwork(t)
	target := fp(9.0)
	in.Target = target
	job, err := Calculate(in)
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	// both branches length 1+4 then +2 = 7; two critical paths
	if len(job.CriticalPaths) != 2 {
		t.Fatalf("want 2 critical paths, got %v", job.CriticalPaths)
	}
	if job.PERT == nil {
		t.Fatal("expected PERT result")
	}
	if job.PERT.MeanDuration != 7 {
		t.Fatalf("mean = %v, want 7", job.PERT.MeanDuration)
	}
	// max-variance critical path must be the C branch (variance 1 > 4/9)
	wantSel := []string{"S", "C", "D"}
	if strings.Join(job.PERT.SelectedPath, ",") != strings.Join(wantSel, ",") {
		t.Fatalf("selected path = %v, want %v", job.PERT.SelectedPath, wantSel)
	}
	if math.Abs(job.PERT.Variance-1) > 1e-9 {
		t.Fatalf("project variance = %v, want 1 (deterministic S,D add 0)", job.PERT.Variance)
	}
	if math.Abs(job.PERT.StdDev-1) > 1e-9 {
		t.Fatalf("stddev = %v, want 1", job.PERT.StdDev)
	}
	if job.PERT.Probability == nil {
		t.Fatal("expected completion probability for target=9")
	}
	if p := *job.PERT.Probability; p <= 0.5 {
		t.Fatalf("target 9 > mean 7 must give probability > 0.5, got %v", p)
	}
	if !strings.Contains(job.PERT.ProbabilityText, "Phi") {
		t.Fatalf("probability formula must be documented in result: %q", job.PERT.ProbabilityText)
	}
	assertFloatIdentities(t, job)
	assertPathSpansSourceToSink(t, in, job)
}

func TestPERTTargetBeforeMeanBelowHalf(t *testing.T) {
	in := pertNetwork(t)
	in.Target = fp(4.0)
	job, err := Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if p := *job.PERT.Probability; p >= 0.5 {
		t.Fatalf("target earlier than mean: probability must be < 0.5, got %v", p)
	}
	// exact mean -> 0.5
	in.Target = fp(7.0)
	job, err = Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if p := *job.PERT.Probability; math.Abs(p-0.5) > 1e-9 {
		t.Fatalf("target == mean: probability %v, want 0.5", p)
	}
}

func TestPERTNoTargetLeavesProbabilityEmpty(t *testing.T) {
	in := pertNetwork(t)
	job, err := Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if job.PERT == nil || job.PERT.Target != nil || job.PERT.Probability != nil {
		t.Fatalf("without target, probability fields must be empty: %+v", job.PERT)
	}
}

// Non-critical activity width changes: project variance must NOT change.
// Critical activity width changes: project variance MUST change.
func TestVarianceSensitivity(t *testing.T) {
	base := NetworkInput{
		Name: "sensitivity",
		Activities: []ActivityInput{
			{ID: "S", Duration: fp(1)},
			// critical branch
			{ID: "C", ThreePoint: tp(1, 4, 7), Pred: []string{"S"}}, // mean 4 var 1
			// non-critical parallel branch (mean 2, ends before merge)
			{ID: "N", ThreePoint: tp(1, 2, 3), Pred: []string{"S"}},
			{ID: "D", Duration: fp(2), Pred: []string{"C", "N"}},
		},
	}
	job, err := Calculate(base)
	if err != nil {
		t.Fatal(err)
	}
	v0 := job.PERT.Variance
	for _, a := range job.Activities {
		if a.ID == "N" && a.Critical {
			t.Fatal("N should be non-critical")
		}
	}

	// Widen the NON-critical three-point estimate while preserving its mean 2:
	// (0.1 + 4*2 + 3.9)/6 = 2.0, only the P-O width grows.
	wide := NetworkInput{
		Name: "sensitivity-wide-noncritical",
		Activities: []ActivityInput{
			{ID: "S", Duration: fp(1)},
			{ID: "C", ThreePoint: tp(1, 4, 7), Pred: []string{"S"}},
			{ID: "N", ThreePoint: tp(0.1, 2.0, 3.9), Pred: []string{"S"}},
			{ID: "D", Duration: fp(2), Pred: []string{"C", "N"}},
		},
	}
	j2, err := Calculate(wide)
	if err != nil {
		t.Fatal(err)
	}
	if len(j2.CriticalPaths) != 1 || strings.Join(j2.CriticalPaths[0], ",") != "S,C,D" {
		t.Fatalf("critical path changed unexpectedly: %v", j2.CriticalPaths)
	}
	if math.Abs(j2.PERT.Variance-v0) > 1e-9 {
		t.Fatalf("project variance changed after widening NON-critical activity: %v -> %v", v0, j2.PERT.Variance)
	}

	// Widen the CRITICAL three-point estimate keeping mean 4:
	// (0.5 + 4*4 + 7.5)/6 = 4.0, width 7 > 6.
	critWide := base
	critWide.Name = "sensitivity-wide-critical"
	critWide.Activities[1].ThreePoint = tp(0.5, 4, 7.5)
	j3, err := Calculate(critWide)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(j3.PERT.Variance-v0) <= 1e-9 {
		t.Fatalf("project variance did NOT change after widening critical activity: still %v", v0)
	}
	if j3.PERT.Variance < v0 {
		t.Fatalf("widened critical variance %v should exceed original %v", j3.PERT.Variance, v0)
	}
}

func TestRejectUnknownPredecessor(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A", Duration: fp(1), Pred: []string{"GHOST"}},
	}}
	_, err := Calculate(in)
	expectErrorKind(t, err, ErrUnknownPred)
}

func TestRejectSelfPrecedence(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A", Duration: fp(1), Pred: []string{"A"}},
	}}
	_, err := Calculate(in)
	expectErrorKind(t, err, ErrSelfPrecedence)
}

func TestRejectDirectedCycle(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A", Duration: fp(1), Pred: []string{"C"}},
		{ID: "B", Duration: fp(1), Pred: []string{"A"}},
		{ID: "C", Duration: fp(1), Pred: []string{"B"}},
	}}
	_, err := Calculate(in)
	expectErrorKind(t, err, ErrCycle)

	// self-loop already covered separately; also a 2-node cycle
	in2 := NetworkInput{Activities: []ActivityInput{
		{ID: "X", Duration: fp(1), Pred: []string{"Y"}},
		{ID: "Y", Duration: fp(1), Pred: []string{"X"}},
	}}
	_, err = Calculate(in2)
	expectErrorKind(t, err, ErrCycle)
}

func TestRejectNonPositiveDuration(t *testing.T) {
	for _, d := range []float64{0, -3} {
		in := NetworkInput{Activities: []ActivityInput{
			{ID: "A", Duration: fp(d)},
		}}
		_, err := Calculate(in)
		expectErrorKind(t, err, ErrBadDuration)
	}
	// zero-duration milestones are explicitly not activities
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "M", Duration: fp(0)},
	}}
	_, err := Calculate(in)
	expectErrorKind(t, err, ErrBadDuration)
}

func TestRejectReversedThreePoint(t *testing.T) {
	cases := []ThreePoint{
		{6, 4, 2}, // fully reversed
		{4, 2, 6}, // most likely < optimistic
		{2, 6, 4}, // most likely > pessimistic
		{0, 4, 6}, // non-positive
	}
	for i, c := range cases {
		in := NetworkInput{Activities: []ActivityInput{
			{ID: "A", ThreePoint: &c},
		}}
		_, err := Calculate(in)
		if err == nil {
			t.Fatalf("case %d (%+v) should be rejected", i, c)
		}
		ve, ok := err.(*ValidationError)
		if !ok || (ve.Kind != ErrBadThreePoint && ve.Kind != ErrBadDuration) {
			t.Fatalf("case %d: wrong error %v", i, err)
		}
	}

	// equal triple is legal (degenerate, zero variance)
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A", ThreePoint: tp(3, 3, 3)},
	}}
	job, err := Calculate(in)
	if err != nil {
		t.Fatalf("equal triple should be legal: %v", err)
	}
	if job.PERT.Variance != 0 || job.PERT.StdDev != 0 {
		t.Fatalf("equal triple variance %v stddev %v, want 0/0", job.PERT.Variance, job.PERT.StdDev)
	}
}

func TestRejectDuplicateAndMissingID(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A", Duration: fp(1)},
		{ID: "A", Duration: fp(2)},
	}}
	_, err := Calculate(in)
	expectErrorKind(t, err, ErrDuplicateID)

	in2 := NetworkInput{Activities: []ActivityInput{
		{ID: "", Duration: fp(1)},
	}}
	_, err = Calculate(in2)
	expectErrorKind(t, err, ErrMissingID)
}

func TestRejectEmptyNetwork(t *testing.T) {
	_, err := Calculate(NetworkInput{})
	expectErrorKind(t, err, ErrEmptyNetwork)
}

func TestRejectMissingAndDoubleEstimate(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{{ID: "A"}}}
	_, err := Calculate(in)
	expectErrorKind(t, err, ErrMissingEstimate)

	d := 2.0
	in2 := NetworkInput{Activities: []ActivityInput{
		{ID: "A", Duration: &d, ThreePoint: tp(1, 2, 3)},
	}}
	_, err = Calculate(in2)
	expectErrorKind(t, err, ErrMissingEstimate)
}

func TestParallelMergeTakesLaterBranch(t *testing.T) {
	// Two source branches merge; the earlier branch carries float.
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "FAST", Duration: fp(2)},
		{ID: "SLOW", Duration: fp(5)},
		{ID: "M", Duration: fp(1), Pred: []string{"FAST", "SLOW"}},
	}}
	job, err := Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if job.ProjectDuration != 6 {
		t.Fatalf("duration %v want 6", job.ProjectDuration)
	}
	for _, a := range job.Activities {
		switch a.ID {
		case "FAST":
			if a.ES != 0 || a.EF != 2 || a.LS != 3 || a.LF != 5 || a.TotalFloat != 3 {
				t.Errorf("FAST params wrong: %+v", a)
			}
		case "SLOW":
			if a.ES != 0 || a.LS != 0 || a.TotalFloat != 0 || !a.Critical {
				t.Errorf("SLOW should be critical: %+v", a)
			}
		case "M":
			if a.ES != 5 {
				t.Errorf("merge ES = %v, want max EF = 5", a.ES)
			}
		}
	}
	assertFloatIdentities(t, job)
}

// Two disconnected chains of equal length: each runs independently source ->
// sink and both must be listed as critical paths (virtual nodes close them).
func TestDisconnectedChainsBothCritical(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A1", Duration: fp(3)},
		{ID: "A2", Duration: fp(2), Pred: []string{"A1"}},
		{ID: "B1", Duration: fp(4)},
		{ID: "B2", Duration: fp(1), Pred: []string{"B1"}},
	}}
	job, err := Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if job.ProjectDuration != 5 {
		t.Fatalf("duration %v want 5 (both independent chains are 5)", job.ProjectDuration)
	}
	got := map[string]bool{}
	for _, p := range job.CriticalPaths {
		got[strings.Join(p, "-")] = true
	}
	if !got["A1-A2"] || !got["B1-B2"] {
		t.Fatalf("critical paths = %v, want both A1-A2 and B1-B2", job.CriticalPaths)
	}
	for _, a := range job.Activities {
		if !a.Critical || a.TotalFloat != 0 {
			t.Fatalf("%s must be critical with TF 0, got crit=%v tf=%v", a.ID, a.Critical, a.TotalFloat)
		}
	}
	assertFloatIdentities(t, job)
	assertPathSpansSourceToSink(t, in, job)

	// A shorter third chain floats against the 5-day deadline.
	in3 := NetworkInput{Activities: []ActivityInput{
		{ID: "A1", Duration: fp(3)},
		{ID: "A2", Duration: fp(2), Pred: []string{"A1"}},
		{ID: "C1", Duration: fp(2)},
	}}
	job3, err := Calculate(in3)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range job3.Activities {
		if a.ID == "C1" && (a.Critical || a.TotalFloat != 3) {
			t.Fatalf("C1 short chain: crit=%v tf=%v, want false/3", a.Critical, a.TotalFloat)
		}
	}
	assertFloatIdentities(t, job3)
}

// Three-point formulas: mean (O+4M+P)/6, variance ((P-O)/6)^2.
func TestThreePointFormulas(t *testing.T) {
	in := NetworkInput{Activities: []ActivityInput{
		{ID: "A", ThreePoint: tp(2, 5, 8)},
	}}
	job, err := Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	a := job.Activities[0]
	if a.Duration != 5 {
		t.Fatalf("expected mean 5, got %v", a.Duration)
	}
	if a.Variance == nil || math.Abs(*a.Variance-1) > 1e-12 {
		t.Fatalf("variance = %v, want ((8-2)/6)^2 = 1", a.Variance)
	}
}
