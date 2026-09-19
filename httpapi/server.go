// Package httpapi exposes the CPM kernel over HTTP. It is the only layer
// talking to the network; the kernel and file store stay transport agnostic.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"cpm"
	"cpm/store"
)

// NewServer wires the kernel and the file repository into a single mux.
func NewServer(jobs *store.FileStore) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", index)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var in cpm.NetworkInput
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "MALFORMED_JSON", err.Error())
			return
		}
		job, err := cpm.Calculate(in)
		if err != nil {
			writeKernelError(w, err)
			return
		}
		job.Source = "submitted"
		saved, err := jobs.Save(r.Context(), job)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "STORE_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	})
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		nums, err := jobs.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "STORE_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job_numbers": nums})
	})
	mux.HandleFunc("GET /jobs/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "INVALID_JOB_NUMBER", "job number must be a positive integer")
			return
		}
		job, err := jobs.Get(r.Context(), n)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "JOB_NOT_FOUND", "no job with number "+strconv.Itoa(n))
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "STORE_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, job)
	})
	mux.HandleFunc("GET /demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, demoPayload())
	})
	mux.HandleFunc("POST /demo/jobs", func(w http.ResponseWriter, r *http.Request) {
		in := cpm.DemoNetwork()
		job, err := cpm.Calculate(in)
		if err != nil {
			writeKernelError(w, err)
			return
		}
		job.Source = "built-in-demo"
		saved, err := jobs.Save(r.Context(), job)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "STORE_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	})

	return logRequests(mux)
}

func index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown endpoint: "+r.URL.Path)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "cpm-scheduler",
		"endpoints": []map[string]string{
			{"method": "POST", "path": "/jobs", "description": "submit a network, compute schedule and persist a job"},
			{"method": "GET", "path": "/jobs/{n}", "description": "fetch a persisted job by number"},
			{"method": "GET", "path": "/jobs", "description": "list job numbers"},
			{"method": "GET", "path": "/demo", "description": "inspect the built-in parallel civil engineering demo network"},
			{"method": "POST", "path": "/demo/jobs", "description": "compute and persist the demo network"},
			{"method": "GET", "path": "/healthz", "description": "liveness probe"},
		},
	})
}

func demoPayload() map[string]any {
	in := cpm.DemoNetwork()
	job, err := cpm.Calculate(in)
	if err != nil {
		// the built-in demo is itself asserted correct by the tests
		panic(err)
	}
	return map[string]any{
		"network": in,
		"hand_check": map[string]any{
			"paths": []map[string]any{
				{"path": []string{"A", "B", "C1", "D", "E"}, "duration": 22.0},
				{"path": []string{"A", "B", "C2", "D", "E"}, "duration": 24.0},
			},
			"critical_path":    []string{"A", "B", "C2", "D", "E"},
			"project_duration": 24.0,
			"C1_total_float":   2.0,
			"note":             "C1 finishes the parallel branch early (EF 12 < C2 EF 14); its total float 2 stays positive, so it is not critical",
		},
		"computed": job,
	}
}

func writeKernelError(w http.ResponseWriter, err error) {
	var ve *cpm.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusBadRequest, string(ve.Kind), ve.Message)
		return
	}
	writeError(w, http.StatusBadRequest, "REJECTED", err.Error())
}

type errorBody struct {
	Error struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, kind, msg string) {
	var b errorBody
	b.Error.Kind = kind
	b.Error.Message = msg
	writeJSON(w, status, b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Reject anything beyond the single JSON document.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON document")
	}
	return nil
}
