package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestAISphereMiddlewareAuthenticatesSessionCookie(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/sessions/introspect" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Aisphere-Service-Token"); got != "test-service-token" {
			t.Fatalf("service token = %q", got)
		}
		_, _ = w.Write([]byte(`{"active":true,"principal":{"subjectId":"admin/alice","username":"alice","organization":"admin","roles":["member"],"sessionId":"sess_alice"}}`))
	}))
	defer authServer.Close()

	cfg := runtimeconfig.AuthConfig{Mode: "aisphere", AISphere: runtimeconfig.AISphereAuthConfig{
		Endpoint:     authServer.URL,
		ServiceToken: "test-service-token",
		CookieName:   "aisphere_session",
		App:          "agentkit",
	}}
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromContext(r.Context())
		if p.UserID != "alice" || p.TenantID != "admin" || p.SessionID != "sess_alice" {
			t.Fatalf("principal = %#v", p)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "aisphere_session", Value: "sess_alice"})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
}

func TestAISphereMiddlewareRejectsMissingSessionCookie(t *testing.T) {
	h := Middleware(runtimeconfig.AuthConfig{Mode: "aisphere", AISphere: runtimeconfig.AISphereAuthConfig{Endpoint: "http://auth.example"}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run")
	}))

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestAISphereMiddlewareAllowsLoginRouteWithoutSession(t *testing.T) {
	h := Middleware(runtimeconfig.AuthConfig{Mode: "aisphere"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
}

func TestAISphereMiddlewareAllowsPublicRuntimeMetadata(t *testing.T) {
	for _, path := range []string{"/models", "/tools", "/builder/defaults"} {
		t.Run(path, func(t *testing.T) {
			h := Middleware(runtimeconfig.AuthConfig{Mode: "aisphere"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			res := httptest.NewRecorder()
			h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
			if res.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
			}
		})
	}
}
