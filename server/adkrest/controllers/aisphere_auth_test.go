package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestAISphereLoginHandlerRedirectsToAuthService(t *testing.T) {
	h := NewAISphereAuthAPIController(&runtimeconfig.Config{Server: runtimeconfig.ServerConfig{API: runtimeconfig.APIConfig{FrontendAddress: "http://localhost:4200"}}, Auth: runtimeconfig.AuthConfig{AISphere: runtimeconfig.AISphereAuthConfig{
		Endpoint: "http://localhost:18080",
		App:      "agentkit",
	}}})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/login?redirect=/api/me", nil)
	res := httptest.NewRecorder()

	h.LoginHandler(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusFound)
	}
	if got, want := res.Header().Get("Location"), "http://localhost:18080/auth/login?app=agentkit&redirect=http%3A%2F%2Flocalhost%3A4200%2Fapi%2Fme"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}

func TestAISphereLogoutHandlerRevokesSessionAndClearsBrowserCookie(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/logout" {
			t.Fatalf("request = %s %s, want POST /auth/logout", r.Method, r.URL.Path)
		}
		if got, want := r.URL.Query().Get("global"), "true"; got != want {
			t.Fatalf("global = %q, want %q", got, want)
		}
		if _, err := r.Cookie("aisphere_session"); err != nil {
			t.Fatalf("session cookie not forwarded: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","logout_url":"http://casdoor.example/logout"}`))
	}))
	defer auth.Close()

	h := NewAISphereAuthAPIController(&runtimeconfig.Config{Server: runtimeconfig.ServerConfig{API: runtimeconfig.APIConfig{FrontendAddress: "http://localhost:4200"}}, Auth: runtimeconfig.AuthConfig{AISphere: runtimeconfig.AISphereAuthConfig{
		Endpoint:   auth.URL,
		App:        "agentkit",
		CookieName: "aisphere_session",
	}}})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout?global=true", nil)
	req.AddCookie(&http.Cookie{Name: "aisphere_session", Value: "sess_123"})
	res := httptest.NewRecorder()

	h.LogoutHandler(res, req)

	if got, want := res.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if cookies := res.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "aisphere_session" || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %#v, want expired aisphere_session", cookies)
	}
	var body struct {
		LogoutURL string `json:"logout_url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, want := body.LogoutURL, "http://casdoor.example/logout"; got != want {
		t.Fatalf("logout_url = %q, want %q", got, want)
	}
}

func TestAISphereLoginHandlerDefaultsToConfiguredFrontend(t *testing.T) {
	h := NewAISphereAuthAPIController(&runtimeconfig.Config{Server: runtimeconfig.ServerConfig{API: runtimeconfig.APIConfig{FrontendAddress: "http://localhost:4200,http://localhost:8080"}}, Auth: runtimeconfig.AuthConfig{AISphere: runtimeconfig.AISphereAuthConfig{
		Endpoint: "http://localhost:18080",
		App:      "agentkit",
	}}})
	res := httptest.NewRecorder()
	h.LoginHandler(res, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))

	if got, want := res.Header().Get("Location"), "http://localhost:18080/auth/login?app=agentkit&redirect=http%3A%2F%2Flocalhost%3A4200%2F"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}
