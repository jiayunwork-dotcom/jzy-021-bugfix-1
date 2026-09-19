package store

import (
	"context"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"cpm"
)

func calculate(t *testing.T, in cpm.NetworkInput) *cpm.Job {
	t.Helper()
	j, err := cpm.Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func fp(x float64) *float64 { return &x }

func tp(o, m, p float64) *cpm.ThreePoint {
	return &cpm.ThreePoint{Optimistic: o, MostLikely: m, Pessimistic: p}
}

// Parallel submissions of different networks must each hold their own
// critical paths: no critical-path leakage between concurrently saved jobs.
func TestParallelJobsDoNotBleed(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Network 1: X->Y critical
	net1 := cpm.NetworkInput{Name: "one", Activities: []cpm.ActivityInput{
		{ID: "X", Duration: fp(3)},
		{ID: "Y", Duration: fp(4), Pred: []string{"X"}},
	}}
	// Network 2: P/Q parallel, Q critical
	net2 := cpm.NetworkInput{Name: "two", Activities: []cpm.ActivityInput{
		{ID: "P", Duration: fp(1)},
		{ID: "Q", Duration: fp(9)},
		{ID: "Z", Duration: fp(2), Pred: []string{"P", "Q"}},
	}}

	const n = 40
	var wg sync.WaitGroup
	jobs := make([]*cpm.Job, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			in := net1
			if i%2 == 1 {
				in = net2
			}
			j, err := cpm.Calculate(in)
			if err != nil {
				errs[i] = err
				return
			}
			saved, err := st.Save(context.Background(), j)
			if err != nil {
				errs[i] = err
				return
			}
			jobs[i] = saved
		}()
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("job %d: %v", i, e)
		}
	}

	// Distinct monotonic numbers 1..n were assigned.
	seen := map[int]bool{}
	for _, j := range jobs {
		if seen[j.Number] {
			t.Fatalf("duplicate job number %d", j.Number)
		}
		seen[j.Number] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct numbers, want %d", len(seen), n)
	}

	// Re-read every job from disk and assert paths match only its own network.
	for i, saved := range jobs {
		got, err := st.Get(context.Background(), saved.Number)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if len(got.CriticalPaths) != 1 ||
				got.CriticalPaths[0][0] != "X" || got.CriticalPaths[0][1] != "Y" {
				t.Fatalf("even job %d contaminated: %v", i, got.CriticalPaths)
			}
			if got.ProjectDuration != 7 {
				t.Fatalf("even job duration %v", got.ProjectDuration)
			}
		} else {
			if len(got.CriticalPaths) != 1 ||
				got.CriticalPaths[0][0] != "Q" || got.CriticalPaths[0][1] != "Z" {
				t.Fatalf("odd job %d contaminated: %v", i, got.CriticalPaths)
			}
			if got.ProjectDuration != 11 {
				t.Fatalf("odd job duration %v", got.ProjectDuration)
			}
		}
	}

	nums, err := st.List(context.Background())
	if err != nil || len(nums) != n {
		t.Fatalf("list = %v, err %v", nums, err)
	}
}

func TestGetMissingJob(t *testing.T) {
	st, _ := NewFileStore(t.TempDir())
	if _, err := st.Get(context.Background(), 999); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Two parallel critical branches (equal means, different widths): a job
// fetched by number must carry exactly the PERT numbers computed at submit
// time. Project variance is the variance along the SELECTED critical path —
// never the sum over every three-point activity in the network.
func TestGetReturnsSubmitTimePERTNumbers(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := cpm.NetworkInput{
		Name: "two-critical-branches",
		Activities: []cpm.ActivityInput{
			{ID: "P", Duration: fp(1)},
			{ID: "Q", ThreePoint: tp(2, 4, 6), Pred: []string{"P"}}, // mean 4, var (4/6)^2 = 4/9
			{ID: "R", ThreePoint: tp(1, 4, 7), Pred: []string{"P"}}, // mean 4, var 1
			{ID: "T", Duration: fp(2), Pred: []string{"Q", "R"}},
		},
		Target: fp(9),
	}
	saved, err := st.Save(context.Background(), calculate(t, in))
	if err != nil {
		t.Fatal(err)
	}

	// Submit-time ground truth: both branches run 1+4+2 = 7 and are critical;
	// the wider branch P-R-T (variance 1 > 4/9) is the selected one.
	if saved.ProjectDuration != 7 {
		t.Fatalf("duration %v, want 7", saved.ProjectDuration)
	}
	if len(saved.CriticalPaths) != 2 {
		t.Fatalf("want 2 critical paths, got %v", saved.CriticalPaths)
	}
	if sel := strings.Join(saved.PERT.SelectedPath, ","); sel != "P,R,T" {
		t.Fatalf("selected path %v, want P,R,T", sel)
	}
	if saved.PERT.Variance != 1 || saved.PERT.StdDev != 1 {
		t.Fatalf("submit variance/stddev = %v/%v, want 1/1", saved.PERT.Variance, saved.PERT.StdDev)
	}

	got, err := st.Get(context.Background(), saved.Number)
	if err != nil {
		t.Fatal(err)
	}

	// The fetched job is the submitted job, value for value.
	if !reflect.DeepEqual(got.Activities, saved.Activities) {
		t.Fatalf("activities changed across save/get:\nsaved %+v\ngot   %+v", saved.Activities, got.Activities)
	}
	if !reflect.DeepEqual(got.PERT, saved.PERT) {
		t.Fatalf("PERT changed across save/get:\nsaved %+v\ngot   %+v", saved.PERT, got.PERT)
	}
	if got.PERT.Variance != 1 || got.PERT.StdDev != 1 {
		t.Fatalf("fetched variance/stddev = %v/%v, want 1/1 (selected path P-R-T only)",
			got.PERT.Variance, got.PERT.StdDev)
	}
	if got.ProjectDuration != 7 {
		t.Fatalf("fetched duration %v, want 7", got.ProjectDuration)
	}

	// Per-path variance list survives the round trip: P-R-T 1, P-Q-T 4/9.
	byPath := map[string]float64{}
	for _, pv := range got.PERT.AllPathVariances {
		byPath[strings.Join(pv.Path, ",")] = pv.Variance
	}
	if len(byPath) != 2 || byPath["P,R,T"] != 1 || math.Abs(byPath["P,Q,T"]-4.0/9.0) > 1e-9 {
		t.Fatalf("fetched path variances = %v, want P,R,T=1 and P,Q,T=4/9", byPath)
	}

	// Fetched variance equals the sum of activity variances along the
	// selected path — and only along it.
	onPath := map[string]bool{}
	for _, id := range got.PERT.SelectedPath {
		onPath[id] = true
	}
	pathSum := 0.0
	for _, a := range got.Activities {
		if a.Variance != nil && onPath[a.ID] {
			pathSum += *a.Variance
		}
	}
	if math.Abs(got.PERT.Variance-pathSum) > 1e-9 {
		t.Fatalf("fetched variance %v != selected-path sum %v", got.PERT.Variance, pathSum)
	}

	// Completion probability stays consistent with the fetched stddev:
	// Phi((9-7)/1) ≈ 0.9772.
	if got.PERT.Probability == nil || got.PERT.Target == nil {
		t.Fatal("fetched job lost target/probability")
	}
	z := (*got.PERT.Target - got.PERT.MeanDuration) / got.PERT.StdDev
	phi := 0.5 * (1 + math.Erf(z/math.Sqrt2))
	if math.Abs(*got.PERT.Probability-phi) > 1e-9 {
		t.Fatalf("probability %v inconsistent with fetched stddev: Phi(%v) = %v",
			*got.PERT.Probability, z, phi)
	}
	if p := *got.PERT.Probability; p < 0.977 || p > 0.978 {
		t.Fatalf("probability %v, want ≈0.9772", p)
	}
}

// Widening a NON-critical three-point branch must not move the project
// variance — neither at submit time nor after a save/get round trip. Both
// jobs live in the same store, so this also pins job-to-job isolation.
func TestGetVarianceImmuneToNonCriticalWidth(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	build := func(name string, o, p float64) cpm.NetworkInput {
		return cpm.NetworkInput{
			Name: name,
			Activities: []cpm.ActivityInput{
				{ID: "P", Duration: fp(1)},
				{ID: "R", ThreePoint: tp(1, 4, 7), Pred: []string{"P"}}, // critical, var 1
				{ID: "N", ThreePoint: tp(o, 2, p), Pred: []string{"P"}}, // non-critical, mean 2
				{ID: "T", Duration: fp(2), Pred: []string{"R", "N"}},
			},
		}
	}
	// Narrow N (1..3, var 1/9) and widened N (0.1..3.9, var ≈ 0.401) with the
	// same mean 2, so the critical path P-R-T is untouched.
	narrow, err := st.Save(context.Background(), calculate(t, build("narrow-n", 1, 3)))
	if err != nil {
		t.Fatal(err)
	}
	wide, err := st.Save(context.Background(), calculate(t, build("wide-n", 0.1, 3.9)))
	if err != nil {
		t.Fatal(err)
	}
	if narrow.Number == wide.Number {
		t.Fatalf("jobs share number %d", narrow.Number)
	}

	for _, saved := range []*cpm.Job{narrow, wide} {
		if saved.PERT.Variance != 1 {
			t.Fatalf("job %d submit variance %v, want 1 (critical path P-R-T only)",
				saved.Number, saved.PERT.Variance)
		}
		got, err := st.Get(context.Background(), saved.Number)
		if err != nil {
			t.Fatal(err)
		}
		if sel := strings.Join(got.PERT.SelectedPath, ","); sel != "P,R,T" {
			t.Fatalf("job %d fetched selected path %v, want P,R,T", saved.Number, sel)
		}
		if got.PERT.Variance != 1 || got.PERT.StdDev != 1 {
			t.Fatalf("job %d fetched variance/stddev = %v/%v, want 1/1 — "+
				"non-critical branch width leaked into the project variance",
				saved.Number, got.PERT.Variance, got.PERT.StdDev)
		}
	}
}

// Control case: a single three-point activity agreed between submit and fetch
// even before the fix — it must keep agreeing.
func TestGetSingleThreePointRoundTrip(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := cpm.NetworkInput{
		Activities: []cpm.ActivityInput{{ID: "A", ThreePoint: tp(2, 5, 8)}},
		Target:     fp(6),
	}
	saved, err := st.Save(context.Background(), calculate(t, in))
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(context.Background(), saved.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got.PERT.Variance != 1 || got.PERT.StdDev != 1 {
		t.Fatalf("fetched variance/stddev = %v/%v, want 1/1", got.PERT.Variance, got.PERT.StdDev)
	}
	if got.PERT.Probability == nil || *got.PERT.Probability != *saved.PERT.Probability {
		t.Fatalf("probability changed across save/get: %v -> %v",
			saved.PERT.Probability, got.PERT.Probability)
	}
	if p := *got.PERT.Probability; math.Abs(p-0.8413) > 1e-3 {
		t.Fatalf("probability %v, want ≈0.8413 (Phi(1))", p)
	}
}
