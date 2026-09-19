// Package store persists planning jobs as local JSON files. No database
// process is involved: one file per job, numbered monotonically, written
// atomically (temp file + rename) and guarded by a single mutex so parallel
// HTTP submissions cannot interleave ids or corrupt files.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cpm"
)

var ErrNotFound = errors.New("job not found")

// FileStore is a directory backed job repository.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore creates the directory if needed and loads existing job ids.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

const jobFilePrefix = "job-"
const jobFileExt = ".json"

// Save assigns the next job number, timestamps and atomically persists job.
// Each submitted network gets its own file; jobs never share state.
func (s *FileStore) Save(ctx context.Context, job *cpm.Job) (*cpm.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next, err := s.nextNumberLocked()
	if err != nil {
		return nil, err
	}
	stored := *job
	stored.Number = next
	if stored.SubmittedAt.IsZero() {
		stored.SubmittedAt = time.Now().UTC()
	}

	if err := s.writeAtomicLocked(ctx, &stored); err != nil {
		return nil, err
	}
	return &stored, nil
}

// Get reads one job by number.
func (s *FileStore) Get(ctx context.Context, number int) (*cpm.Job, error) {
	name := fmt.Sprintf("%s%d%s", jobFilePrefix, number, jobFileExt)
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var job cpm.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	// The persisted PERT result is authoritative: project variance is the sum
	// along the selected critical path (see cpm.selectPERTPath), and the
	// completion probability was computed against that standard deviation.
	// It must NOT be recomputed here by summing every three-point activity in
	// the network — that would pull in non-critical branches and the critical
	// paths that lost the max-variance selection, silently contradicting both
	// selected_critical_path and all_critical_path_variances.
	return &job, nil
}

// List returns the numbers of persisted jobs in ascending order.
func (s *FileStore) List(ctx context.Context) ([]int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var nums []int
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, jobFilePrefix) || !strings.HasSuffix(n, jobFileExt) {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(n, jobFilePrefix), jobFileExt)
		v, err := strconv.Atoi(mid)
		if err != nil {
			continue
		}
		nums = append(nums, v)
	}
	sort.Ints(nums)
	return nums, nil
}

func (s *FileStore) nextNumberLocked() (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	max := 0
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, jobFilePrefix) || !strings.HasSuffix(n, jobFileExt) {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(n, jobFilePrefix), jobFileExt)
		if v, err := strconv.Atoi(mid); err == nil && v > max {
			max = v
		}
	}
	return max + 1, nil
}

// writeAtomicLocked serializes to a temp file in the same directory and renames
// over the target, so a crashed reader can never observe a half-written job.
func (s *FileStore) writeAtomicLocked(ctx context.Context, job *cpm.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(s.dir, fmt.Sprintf("%s%d%s", jobFilePrefix, job.Number, jobFileExt))
	tmp, err := os.CreateTemp(s.dir, ".job-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, final)
}
