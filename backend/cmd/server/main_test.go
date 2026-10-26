package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"reading-tracker/backend/internal/wire"
)

type stubPinger struct{ err error }

func (s *stubPinger) Ping(context.Context) error { return s.err }

func TestHealthzReports503WhenDatabaseDown(t *testing.T) {
	p := &stubPinger{err: errors.New("db down")}
	h := wire.Healthz(p)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "unavailable" {
		t.Fatalf("down: got %d %q", rec.Code, rec.Body.String())
	}
	p.err = nil
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("up: got %d %q", rec.Code, rec.Body.String())
	}
}

// Final review F3: liveness is process-only, so a database outage never restarts pods.
func TestLivezAlwaysOK(t *testing.T) {
	rec := httptest.NewRecorder()
	wire.Livez()(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("got %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}
