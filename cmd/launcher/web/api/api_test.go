package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorsWithArgsAllowsConfiguredOriginToSendCookies(t *testing.T) {
	h := corsWithArgs("http://localhost:4200")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	req.Header.Set("Origin", "http://localhost:4200")
	res := httptest.NewRecorder()

	h.ServeHTTP(res, req)

	if got, want := res.Header().Get("Access-Control-Allow-Origin"), "http://localhost:4200"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	if got, want := res.Header().Get("Access-Control-Allow-Credentials"), "true"; got != want {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want %q", got, want)
	}
}
