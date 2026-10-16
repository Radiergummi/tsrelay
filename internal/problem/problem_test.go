package problem

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteAndDecode(t *testing.T) {
	rec := httptest.NewRecorder()
	p := New(StatusTunnelUnavailable, TunnelUnavailable, "no tunnel is attached")
	p.Errors = []map[string]string{{"field": "path"}}
	p.Write(rec)

	resp := rec.Result()
	if got := resp.Header.Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get(HeaderError); got != TunnelUnavailable {
		t.Fatalf("%s = %q", HeaderError, got)
	}
	err := Decode(resp)
	var got *Problem
	if !errors.As(err, &got) {
		t.Fatalf("Decode returned %T %v, want *Problem", err, err)
	}
	if got.Status != 561 || got.Code != TunnelUnavailable || got.Type != "https://tsrelay.mazetti.me/errors/tunnel_unavailable" || got.Title != "Tunnel Unavailable" {
		t.Fatalf("decoded %+v", got)
	}
}

func TestDecodeNonProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	http.Error(rec, "boom", http.StatusBadGateway)
	err := Decode(rec.Result())
	var p *Problem
	if errors.As(err, &p) {
		t.Fatal("plain text body must not decode as a problem")
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q should mention status and body", err)
	}
}
