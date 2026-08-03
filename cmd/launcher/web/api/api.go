// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package api provides a sublauncher that adds ADK REST API capabilities.
package api

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"google.golang.org/adk/cmd/launcher"
	weblauncher "google.golang.org/adk/cmd/launcher/web"
	"google.golang.org/adk/internal/cli/util"
	"google.golang.org/adk/server/adkrest"
	"google.golang.org/adk/telemetry"
)

// apiConfig contains parametres for lauching ADK REST API
type apiConfig struct {
	frontendAddress string
	pathPrefix      string
	sseWriteTimeout time.Duration
	traceCapacity   int
}

// apiLauncher can launch ADK REST API
type apiLauncher struct {
	flags  *flag.FlagSet
	config *apiConfig
}

// CommandLineSyntax returns the command-line syntax for the API launcher.
func (a *apiLauncher) CommandLineSyntax() string {
	return util.FormatFlagUsage(a.flags)
}

// Adds CORS headers which allow calling ADK REST API from another web app
// such as a separately started ADK Web UI dev server.
//
// frontendAddress accepts a comma-separated allow-list. Each entry should be a
// browser origin such as "http://localhost:4200". For backwards compatibility,
// entries without a scheme, such as "localhost:8080", are normalized to both
// "http://localhost:8080" and "https://localhost:8080". Use "*" to allow all
// origins for local development.
func corsWithArgs(frontendAddress string) func(next http.Handler) http.Handler {
	allowedOrigins := parseAllowedOrigins(frontendAddress)
	allowAll := allowedOrigins["*"]

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" {
				if allowedOrigins[origin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			} else if frontendAddress != "" {
				// Non-browser clients do not send Origin, but setting a valid value is
				// still useful for inspection and avoids the old invalid value
				// "localhost:8080".
				if first := firstAllowedOrigin(allowedOrigins); first != "" {
					w.Header().Set("Access-Control-Allow-Origin", first)
				}
			}
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, Accept")
			w.Header().Set("Access-Control-Expose-Headers", "X-ADK-Run-ID")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func parseAllowedOrigins(frontendAddress string) map[string]bool {
	origins := map[string]bool{}
	for _, raw := range strings.Split(frontendAddress, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if raw == "*" {
			origins["*"] = true
			continue
		}
		raw = strings.TrimRight(raw, "/")
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			origins[raw] = true
			continue
		}
		origins["http://"+raw] = true
		origins["https://"+raw] = true
	}
	return origins
}

func firstAllowedOrigin(origins map[string]bool) string {
	for origin := range origins {
		if origin != "*" {
			return origin
		}
	}
	return ""
}

// UserMessage implements web.Sublauncher. Prints message to the user
func (a *apiLauncher) UserMessage(webURL string, printer func(v ...any)) {
	printer(fmt.Sprintf("       api:  you can access API using %s%s", webURL, a.config.pathPrefix))
	printer(fmt.Sprintf("       api:      for instance: %s%s/list-apps", webURL, a.config.pathPrefix))
}

// SetupSubrouters adds the API router to the parent router.
func (a *apiLauncher) SetupSubrouters(router *mux.Router, config *launcher.Config) error {
	// Create the ADK REST API handler
	restServer, err := adkrest.NewServer(adkrest.ServerConfig{
		SessionService:      config.SessionService,
		MemoryService:       config.MemoryService,
		AgentLoader:         config.AgentLoader,
		ArtifactService:     config.ArtifactService,
		SSEWriteTimeout:     a.config.sseWriteTimeout,
		PluginConfig:        config.PluginConfig,
		BuilderAppsRoot:     config.BuilderAppsRoot,
		BuilderTmpRoot:      config.BuilderTmpRoot,
		BuilderDefaultModel: config.BuilderDefaultModel,
		RuntimeConfig:       config.RuntimeConfig,
		TraceRecorder:       config.TraceRecorder,
		SkillService:        config.SkillService,
		DebugConfig: adkrest.DebugTelemetryConfig{
			TraceCapacity: a.config.traceCapacity,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create REST server: %w", err)
	}

	config.TelemetryOptions = append(config.TelemetryOptions, telemetry.WithSpanProcessors(restServer.SpanProcessor()), telemetry.WithLogRecordProcessors(restServer.LogProcessor()))

	// Wrap it with CORS middleware
	corsHandler := corsWithArgs(a.config.frontendAddress)(restServer)

	// If prefix is empty, don't use PathPrefix("") because it's too greedy.
	// Instead, attach the handler to the main router directly.
	if a.config.pathPrefix == "" || a.config.pathPrefix == "/" {
		// This allows other routes (like /ui/) to match first if registered
		router.Methods("GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS").Handler(corsHandler)
	} else {
		router.Methods("GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS").
			PathPrefix(a.config.pathPrefix).
			Handler(http.StripPrefix(a.config.pathPrefix, corsHandler))
	}
	return nil
}

// Keyword implements web.Sublauncher. Returns the command-line keyword for API launcher.
func (a *apiLauncher) Keyword() string {
	return "api"
}

// Parse parses the command-line arguments for the API launcher.
func (a *apiLauncher) Parse(args []string) ([]string, error) {
	err := a.flags.Parse(args)
	if err != nil || !a.flags.Parsed() {
		return nil, fmt.Errorf("failed to parse api flags: %v", err)
	}
	p := a.config.pathPrefix
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	a.config.pathPrefix = strings.TrimSuffix(p, "/")

	restArgs := a.flags.Args()
	return restArgs, nil
}

// SimpleDescription implements web.Sublauncher. Returns a simple description of the API launcher.
func (a *apiLauncher) SimpleDescription() string {
	return "starts ADK REST API server, accepting origins specified by webui_address (CORS)"
}

// NewLauncher creates new api launcher. It extends Web launcher
func NewLauncher() weblauncher.Sublauncher {
	config := &apiConfig{}

	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.StringVar(&config.frontendAddress, "webui_address", "localhost:8080", "ADK WebUI address as seen from the user browser. It's used to allow CORS requests. Please specify only hostname and (optionally) port.")
	fs.StringVar(&config.pathPrefix, "path_prefix", "/api", "ADK REST API path prefix. Default is '/api'.")
	fs.DurationVar(&config.sseWriteTimeout, "sse-write-timeout", 120*time.Second, "SSE server write timeout (i.e. '10s', '2m' - see time.ParseDuration for details) - for writing the SSE response after reading the headers & body")
	fs.IntVar(&config.traceCapacity, "trace_capacity", 10000, "Maximum number of traces to keep in memory.")

	return &apiLauncher{
		config: config,
		flags:  fs,
	}
}
