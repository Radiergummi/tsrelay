package api

import (
	"net/http/httptest"
	"testing"
)

func TestQueryBool(t *testing.T) {
	for q, want := range map[string]bool{
		"dryRun=1":    true,
		"dryRun=true": true,
		"dryRun=TRUE": true,
		"dryRun=yes":  false,
		"dryRun=":     false,
		"":            false,
	} {
		if got := queryBool(httptest.NewRequest("POST", "/hooks?"+q, nil), "dryRun"); got != want {
			t.Errorf("queryBool(%q) = %v, want %v", q, got, want)
		}
	}
}
