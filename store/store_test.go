package store

import (
	"context"
	"math"
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

func tpo(o, m, p float64) *cpm.ThreePoint {
	return &cpm.ThreePoint{Optimistic: o, MostLikely: m, Pessimistic: p}
}

// saveAndGet runs the exact user-facing round trip: Calculate -> Save (the
// POST /jobs path) -> Get (the GET /jobs/{n} path).
func saveAndGet(t *testing.T, st *FileStore, in cpm.NetworkInput) (*cpm.Job, *cpm.Job) {
	t.Helper()
	saved, err := st.Save(context.Background(), calculate(t, in))
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(context.Background(), saved.Number)
	if err != nil {
		t.Fatal(err)
	}
	return saved, got
}

// normalCDF mirrors the kernel's Phi(z) so a test can recompute the
// completion probability from the fetched standard deviation and verify they
// agree (a stale probability against a rewritten variance would be caught).
func normalCDF(z float64) float64 {
	return 0.5 * (1 + math.Erf(z/math.Sqrt2))
}

// assertFetchedPERTLocksToSelectedPath is the invariant the user asked tests
// to hold after a save/fetch round trip:
//  1. fetched project variance == project variance at submit time;
//  2. it equals the variance of the selected critical path, both as listed in
//     all_critical_path_variances and as the sum of its per-activity values;
//  3. fetched stddev == sqrt(variance), and the selected path itself, the
//     per-path list and the completion probability survive the round trip;
//  4. the probability is still the Phi value for THAT stddev.
func assertFetchedPERTLocksToSelectedPath(t *testing.T, saved, got *cpm.Job) {
	t.Helper()
	if got.PERT == nil || saved.PERT == nil {
		t.Fatal("both jobs must carry PERT results")
	}
	const tol = 1e-9
	sp, gp := saved.PERT, got.PERT

	if math.Abs(gp.Variance-sp.Variance) > tol {
		t.Errorf("fetched project variance %v != submit-time %v", gp.Variance, sp.Variance)
	}
	if math.Abs(gp.StdDev-sp.StdDev) > tol {
		t.Errorf("fetched stddev %v != submit-time %v", gp.StdDev, sp.StdDev)
	}
	if strings.Join(gp.SelectedPath, ",") != strings.Join(sp.SelectedPath, ",") {
		t.Errorf("fetched selected path %v != submit-time %v", gp.SelectedPath, sp.SelectedPath)
	}

	// Variance must equal the selected path's entry in the per-path list.
	var listed float64
	found := false
	selected := strings.Join(gp.SelectedPath, ",")
	for _, pv := range gp.AllPathVariances {
		if strings.Join(pv.Path, ",") == selected {
			listed = pv.Variance
			found = true
		}
	}
	if !found {
		t.Fatalf("selected path %v absent from all_critical_path_variances %+v",
			gp.SelectedPath, gp.AllPathVariances)
	}
	if math.Abs(listed-gp.Variance) > 1e-6 {
		t.Errorf("project variance %v != selected path listed variance %v", gp.Variance, listed)
	}

	// ... and the sum of the per-activity variances recorded for that path.
	sum := 0.0
	for _, id := range gp.SelectedPath {
		sum += gp.PathVariances[id]
	}
	if math.Abs(clean(sum)-gp.Variance) > 1e-6 {
		t.Errorf("project variance %v != sum %v over selected path %v",
			gp.Variance, sum, gp.SelectedPath)
	}

	if math.Abs(math.Sqrt(math.Max(gp.Variance, 0))-gp.StdDev) > tol {
		t.Errorf("fetched stddev %v != sqrt(variance %v)", gp.StdDev, gp.Variance)
	}

	if len(gp.AllPathVariances) != len(sp.AllPathVariances) {
		t.Fatalf("per-path variance list changed through fetch: %v vs %v",
			gp.AllPathVariances, sp.AllPathVariances)
	}

	if gp.Target != nil && gp.Probability != nil {
		z := (*gp.Target - gp.MeanDuration) / gp.StdDev
		wantP := normalCDF(z)
		if math.Abs(*gp.Probability-wantP) > 1e-9 {
			t.Errorf("probability %v is not Phi((target-mean)/stddev)=%v for fetched stddev %v",
				*gp.Probability, wantP, gp.StdDev)
		}
		if gp.Probability != nil && sp.Probability != nil &&
			math.Abs(*gp.Probability-*sp.Probability) > tol {
			t.Errorf("fetched probability %v != submit-time %v", *gp.Probability, *sp.Probability)
		}
	}
}

// clean mirrors cpm.cleanFloat for tolerance purposes (unexported across the
// module boundary): treat round-off dust below 1e-9 as zero.
func clean(x float64) float64 {
	if math.Abs(x) < 1e-9 {
		return 0
	}
	return x
}

// Two parallel critical paths, different widths: the wider path P-R-T
// (variance 1) is selected over P-Q-T (variance 4/9). Fetching the job must
// not sum both branches' variances (1 + 4/9 ~= 1.444).
func TestFetchVarianceEqualsSelectedCriticalPath(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := cpm.NetworkInput{
		Name:   "two-parallel-critical-paths",
		Target: fp(9),
		Activities: []cpm.ActivityInput{
			{ID: "P", Duration: fp(1)},
			{ID: "Q", ThreePoint: tpo(2, 4, 6), Pred: []string{"P"}}, // mean 4, var 4/9
			{ID: "R", ThreePoint: tpo(1, 4, 7), Pred: []string{"P"}}, // mean 4, var 1
			{ID: "T", Duration: fp(2), Pred: []string{"Q", "R"}},
		},
	}
	saved, got := saveAndGet(t, st, in)

	if got.ProjectDuration != 7 {
		t.Fatalf("duration = %v, want 7", got.ProjectDuration)
	}
	if strings.Join(got.PERT.SelectedPath, ",") != "P,R,T" {
		t.Fatalf("selected path = %v, want P,R,T (variance 1 > 4/9)", got.PERT.SelectedPath)
	}
	if math.Abs(got.PERT.Variance-1) > 1e-9 {
		t.Fatalf("fetched project variance = %v, want 1", got.PERT.Variance)
	}
	if math.Abs(got.PERT.StdDev-1) > 1e-9 {
		t.Fatalf("fetched stddev = %v, want 1", got.PERT.StdDev)
	}
	// The loser branch's variance must still be listed separately.
	listed := map[string]float64{}
	for _, pv := range got.PERT.AllPathVariances {
		listed[strings.Join(pv.Path, ",")] = pv.Variance
	}
	if math.Abs(listed["P,Q,T"]-4.0/9.0) > 1e-6 {
		t.Fatalf("P-Q-T path variance = %v, want ~0.4444", listed["P,Q,T"])
	}
	if p := *got.PERT.Probability; math.Abs(p-normalCDF(2)) > 1e-9 {
		t.Fatalf("probability = %v, want Phi(2) ~= 0.9772", p)
	}
	assertFetchedPERTLocksToSelectedPath(t, saved, got)
}

// A single three-point activity: the submit/fetch values were already equal
// (2/5/8, target 6 -> variance 1, stddev 1, p ~= 0.841); keep it locked.
func TestFetchSingleThreePointMatchesSubmit(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := cpm.NetworkInput{
		Name:       "single-three-point",
		Target:     fp(6),
		Activities: []cpm.ActivityInput{{ID: "A", ThreePoint: tpo(2, 5, 8)}},
	}
	saved, got := saveAndGet(t, st, in)
	if math.Abs(got.PERT.Variance-1) > 1e-9 || math.Abs(got.PERT.StdDev-1) > 1e-9 {
		t.Fatalf("fetched variance/stddev = %v/%v, want 1/1",
			got.PERT.Variance, got.PERT.StdDev)
	}
	if math.Abs(*got.PERT.Probability-normalCDF(1)) > 1e-9 {
		t.Fatalf("probability = %v, want Phi(1) ~= 0.8413", *got.PERT.Probability)
	}
	assertFetchedPERTLocksToSelectedPath(t, saved, got)
}

// Widening a NON-critical three-point branch (mean unchanged) must leave the
// fetched project variance exactly where submit time put it: on the selected
// critical path's variance (1), not 1 + the non-critical branch's variance.
func TestFetchVarianceIgnoresNonCriticalThreePointWidth(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	build := func(o, p float64) cpm.NetworkInput {
		return cpm.NetworkInput{
			Name:   "noncritical-width-control",
			Target: fp(9),
			Activities: []cpm.ActivityInput{
				{ID: "P", Duration: fp(1)},
				{ID: "R", ThreePoint: tpo(1, 4, 7), Pred: []string{"P"}}, // critical, var 1
				{ID: "N", ThreePoint: tpo(o, 2, p), Pred: []string{"P"}}, // non-critical, mean 2
				{ID: "T", Duration: fp(2), Pred: []string{"R", "N"}},
			},
		}
	}

	savedNarrow, gotNarrow := saveAndGet(t, st, build(1, 3))
	if strings.Join(gotNarrow.PERT.SelectedPath, ",") != "P,R,T" {
		t.Fatalf("selected path = %v, want P,R,T", gotNarrow.PERT.SelectedPath)
	}
	if math.Abs(gotNarrow.PERT.Variance-1) > 1e-9 {
		t.Fatalf("narrow non-critical branch: fetched variance = %v, want 1",
			gotNarrow.PERT.Variance)
	}
	assertFetchedPERTLocksToSelectedPath(t, savedNarrow, gotNarrow)

	// Widen N to 0.1/2/3.9 (still mean 2): N's own variance grows to
	// (3.8/6)^2 ~= 0.4011 but P-N-T stays non-critical (5 < 7), so the
	// project variance fetched from disk must remain 1.
	savedWide, gotWide := saveAndGet(t, st, build(0.1, 3.9))
	if math.Abs(gotWide.PERT.Variance-1) > 1e-9 {
		t.Fatalf("widened NON-critical branch leaked into fetched project variance: %v, want 1",
			gotWide.PERT.Variance)
	}
	if math.Abs(gotWide.PERT.StdDev-1) > 1e-9 {
		t.Fatalf("widened NON-critical branch leaked into fetched stddev: %v, want 1",
			gotWide.PERT.StdDev)
	}
	nVar := 0.0
	if a := findActivity(gotWide, "N"); a != nil && a.Variance != nil {
		nVar = *a.Variance
	}
	if math.Abs(nVar-(3.8/6.0)*(3.8/6.0)) > 1e-9 {
		t.Fatalf("N's own variance = %v, want ~0.4011 (control: it IS stored per activity)", nVar)
	}
	if !criticalPathsContain(gotWide, "P,R,T") {
		t.Fatalf("critical paths = %v, want P,R,T", gotWide.CriticalPaths)
	}
	assertFetchedPERTLocksToSelectedPath(t, savedWide, gotWide)
}

func findActivity(job *cpm.Job, id string) *cpm.ActivityResult {
	for i := range job.Activities {
		if job.Activities[i].ID == id {
			return &job.Activities[i]
		}
	}
	return nil
}

// criticalPathsContain reports whether a path with the given comma-joined ids
// is listed.
func criticalPathsContain(job *cpm.Job, want string) bool {
	for _, p := range job.CriticalPaths {
		if strings.Join(p, ",") == want {
			return true
		}
	}
	return false
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
