package store

import (
	"context"
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
