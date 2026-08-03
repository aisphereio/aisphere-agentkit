package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/adk/internal/runtimeconfig"
)

// AISphereAuthAPIController starts the browser login flow for an ADK server.
type AISphereAuthAPIController struct {
	cfg *runtimeconfig.Config
}

func NewAISphereAuthAPIController(cfg *runtimeconfig.Config) *AISphereAuthAPIController {
	return &AISphereAuthAPIController{cfg: cfg}
}

func (c *AISphereAuthAPIController) LoginHandler(rw http.ResponseWriter, req *http.Request) {
	if c == nil || c.cfg == nil || strings.TrimSpace(c.cfg.Auth.AISphere.Endpoint) == "" {
		respondError(rw, http.StatusServiceUnavailable, "aisphere authentication is not configured")
		return
	}
	redirect := c.loginRedirect(req.URL.Query().Get("redirect"))
	q := url.Values{"app": {c.cfg.Auth.AISphere.App}, "redirect": {redirect}}
	http.Redirect(rw, req, strings.TrimRight(c.cfg.Auth.AISphere.Endpoint, "/")+"/auth/login?"+q.Encode(), http.StatusFound)
}

// LogoutHandler revokes the AIsphere session and clears the browser cookie.
func (c *AISphereAuthAPIController) LogoutHandler(rw http.ResponseWriter, req *http.Request) {
	if c == nil || c.cfg == nil || strings.TrimSpace(c.cfg.Auth.AISphere.Endpoint) == "" {
		respondError(rw, http.StatusServiceUnavailable, "aisphere authentication is not configured")
		return
	}
	cookieName := c.cfg.Auth.AISphere.CookieName
	if cookieName == "" {
		cookieName = "aisphere_session"
	}
	cookie, err := req.Cookie(cookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		respondError(rw, http.StatusUnauthorized, "missing aisphere session")
		return
	}
	logoutURL, err := url.Parse(strings.TrimRight(c.cfg.Auth.AISphere.Endpoint, "/") + "/auth/logout")
	if err != nil {
		respondError(rw, http.StatusInternalServerError, "invalid aisphere authentication endpoint")
		return
	}
	q := logoutURL.Query()
	if req.URL.Query().Get("global") == "true" {
		q.Set("global", "true")
	}
	q.Set("redirect", c.loginRedirect(""))
	logoutURL.RawQuery = q.Encode()
	authReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, logoutURL.String(), nil)
	if err != nil {
		respondError(rw, http.StatusInternalServerError, "build aisphere logout request")
		return
	}
	authReq.AddCookie(cookie)
	resp, err := (&http.Client{Timeout: time.Duration(c.cfg.Auth.AISphere.TimeoutSeconds) * time.Second}).Do(authReq)
	if err != nil {
		respondError(rw, http.StatusBadGateway, fmt.Sprintf("revoke aisphere session: %v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respondError(rw, http.StatusBadGateway, "aisphere logout failed")
		return
	}
	var result struct {
		LogoutURL string `json:"logout_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		respondError(rw, http.StatusBadGateway, "invalid aisphere logout response")
		return
	}
	http.SetCookie(rw, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0), HttpOnly: true})
	EncodeJSONResponse(map[string]string{"status": "ok", "logout_url": result.LogoutURL}, http.StatusOK, rw)
}

func (c *AISphereAuthAPIController) loginRedirect(raw string) string {
	frontends := c.frontendOrigins()
	if len(frontends) == 0 {
		return "/api/me"
	}
	base := frontends[0]
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return base + "/"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return base + "/"
	}
	if !u.IsAbs() && u.Host == "" && strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(raw, "//") {
		return base + raw
	}
	if u.IsAbs() && isConfiguredFrontend(frontends, u) {
		return raw
	}
	return base + "/"
}

func (c *AISphereAuthAPIController) frontendOrigins() []string {
	if c == nil || c.cfg == nil {
		return nil
	}
	values := strings.Split(c.cfg.Server.API.FrontendAddress, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		u, err := url.Parse(strings.TrimSpace(value))
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			out = append(out, strings.TrimRight(u.String(), "/"))
		}
	}
	return out
}

func isConfiguredFrontend(frontends []string, candidate *url.URL) bool {
	for _, frontend := range frontends {
		u, err := url.Parse(frontend)
		if err == nil && u.Scheme == candidate.Scheme && u.Host == candidate.Host {
			return true
		}
	}
	return false
}
