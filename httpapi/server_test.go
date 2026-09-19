package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cpm"
	"cpm/store"
)

func newTestServer(t *testing.T) (http.Handler, *store.FileStore) {
	t.Helper()
	st, err := store.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(st), st
}

func postJSON(t *testing.T, h http.Handler, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s body: %v\n%s", path, err, rec.Body.String())
		}
	}
	return rec, out
}

func TestSubmitAndFetchJob(t *testing.T) {
	h, _ := newTestServer(t)
	body := map[string]any{
		"name": "http smoke",
		"activities": []map[string]any{
			{"id": "A", "duration": 3},
			{"id": "B", "duration": 5, "predecessors": []string{"A"}},
		},
		"target": 10,
	}
	rec, out := postJSON(t, h, "/jobs", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if num, _ := out["number"].(float64); num != 1 {
		t.Fatalf("job number %v, want 1", out["number"])
	}
	if dur, _ := out["project_duration"].(float64); dur != 8 {
		t.Fatalf("duration %v, want 8", out["project_duration"])
	}
	if _, ok := out["pert"]; ok {
		t.Fatal("deterministic network must not include pert")
	}

	req := httptest.NewRequest(http.MethodGet, "/jobs/1", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET status %d", rec2.Code)
	}
}

func TestTypedErrorsOverHTTP(t *testing.T) {
	h, _ := newTestServer(t)

	// duplicate id
	dup := map[string]any{"activities": []map[string]any{
		{"id": "A", "duration": 1},
		{"id": "A", "duration": 2},
	}}
	rec, out := postJSON(t, h, "/jobs", dup)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dup status %d", rec.Code)
	}
	kind, _ := out["error"].(map[string]any)["kind"].(string)
	if kind != string(cpm.ErrDuplicateID) {
		t.Fatalf("error kind = %q, want DUPLICATE_ID", kind)
	}

	// cycle
	cyc := map[string]any{"activities": []map[string]any{
		{"id": "A", "duration": 1, "predecessors": []string{"B"}},
		{"id": "B", "duration": 1, "predecessors": []string{"A"}},
	}}
	rec, out = postJSON(t, h, "/jobs", cyc)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cycle status %d", rec.Code)
	}
	kind, _ = out["error"].(map[string]any)["kind"].(string)
	if kind != string(cpm.ErrCycle) {
		t.Fatalf("error kind = %q, want DIRECTED_CYCLE", kind)
	}

	// reversed three point
	tp := map[string]any{"activities": []map[string]any{
		{"id": "A", "three_point": map[string]any{"optimistic": 9, "most_likely": 5, "pessimistic": 2}},
	}}
	rec, out = postJSON(t, h, "/jobs", tp)
	kind, _ = out["error"].(map[string]any)["kind"].(string)
	if rec.Code != http.StatusBadRequest || kind != string(cpm.ErrBadThreePoint) {
		t.Fatalf("three point: status=%d kind=%q", rec.Code, kind)
	}

	// missing field -> malformed JSON semantics via unknown field rejection
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"activities":[{"id":"A","duration":1,"bogus":true}]}`))
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", rec3.Code)
	}
}

func TestPERTOverHTTP(t *testing.T) {
	h, _ := newTestServer(t)
	body := map[string]any{
		"name": "pert http",
		"activities": []map[string]any{
			{"id": "A", "three_point": map[string]any{"optimistic": 2, "most_likely": 5, "pessimistic": 8}},
		},
		"target": 6,
	}
	rec, out := postJSON(t, h, "/jobs", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	pert, _ := out["pert"].(map[string]any)
	if pert == nil {
		t.Fatal("missing pert result")
	}
	prob, _ := pert["completion_probability"].(float64)
	if prob <= 0.5 || prob > 1 {
		t.Fatalf("target 6 > mean 5 should give p in (0.5,1], got %v", prob)
	}
	sel, _ := pert["selected_critical_path"].([]any)
	if len(sel) != 1 || sel[0] != "A" {
		t.Fatalf("selected path %v", sel)
	}
}

func TestDemoEndpoints(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/demo", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /demo %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/demo/jobs", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /demo/jobs %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if num, _ := out["number"].(float64); num != 1 {
		t.Fatalf("demo job number %v", out["number"])
	}
}

func TestNotFoundJobAndUnknownRoute(t *testing.T) {
	h, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/jobs/77", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d", rec.Code)
	}
}

func TestJobList(t *testing.T) {
	h, _ := newTestServer(t)
	for range 3 {
		postJSON(t, h, "/jobs", map[string]any{"activities": []map[string]any{
			{"id": "A", "duration": 1},
		}})
	}
	req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d", rec.Code)
	}
	var out struct {
		Numbers []int `json:"job_numbers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Numbers) != 3 || out.Numbers[2] != 3 {
		t.Fatalf("numbers = %v", out.Numbers)
	}
}
