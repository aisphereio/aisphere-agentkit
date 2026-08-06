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

package adkrest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/platform/approvals"
	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/improvements"
	"google.golang.org/adk/internal/platform/novelstore"
	"google.golang.org/adk/internal/platform/objectstore"
	"google.golang.org/adk/internal/platform/projects"
	platformruns "google.golang.org/adk/internal/platform/runs"
	"google.golang.org/adk/internal/platform/store"
	"google.golang.org/adk/internal/platform/uploads"
	"google.golang.org/adk/internal/platform/users"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/sandboxclient"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/internal/skillservice"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adkrest/controllers"
	"google.golang.org/adk/server/adkrest/internal/resumable"
	"google.golang.org/adk/server/adkrest/internal/routers"
	"google.golang.org/adk/server/adkrest/internal/services"
	"google.golang.org/adk/session"
)

// NewServer creates a new ADK REST API server which implements [http.Handler] interface.
func NewServer(cfg ServerConfig) (*Server, error) {
	debugTelemetry, err := services.NewDebugTelemetryWithConfig(&services.DebugTelemetryConfig{
		TraceCapacity: cfg.DebugConfig.TraceCapacity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create debug telemetry service: %w", err)
	}
	var runStore *resumable.Store
	var subAgentObserveStore controllers.SubAgentTaskObserveStore
	if cfg.RuntimeConfig != nil {
		redisCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		runStore, err = resumable.NewRedisStore(redisCtx, cfg.RuntimeConfig.Server.API.ResumableRuns)
		if err != nil {
			return nil, fmt.Errorf("failed to create resumable run store: %w", err)
		}
		observeTTL := parseDurationDefault(cfg.RuntimeConfig.Runtime.SubAgentTasks.ObserveTTL, 6*time.Hour)
		subAgentObserveStore, err = controllers.NewBestEffortSubAgentTaskObserveStore(redisCtx, cfg.RuntimeConfig.Server.API.ResumableRuns, observeTTL)
		if err != nil {
			return nil, fmt.Errorf("failed to create sub-agent observe store: %w", err)
		}
	} else {
		subAgentObserveStore = controllers.NewSubAgentTaskObserveStore(6 * time.Hour)
	}

	platformRunService := cfg.PlatformRunService
	platformApprovalService := cfg.PlatformApprovalService
	platformImprovementService := cfg.PlatformImprovementService
	platformUserService := cfg.PlatformUserService
	platformProjectService := cfg.PlatformProjectService
	platformUploadService := cfg.PlatformUploadService
	platformNovelStoreService := cfg.PlatformNovelStoreService
	if cfg.RuntimeConfig != nil && (platformRunService == nil || platformApprovalService == nil || platformImprovementService == nil || platformUserService == nil || platformProjectService == nil || platformUploadService == nil || platformNovelStoreService == nil) {
		db, err := store.OpenGORM(cfg.RuntimeConfig.Storage.Database)
		if err != nil {
			return nil, fmt.Errorf("failed to open platform database: %w", err)
		}
		if cfg.RuntimeConfig.Storage.Database.AutoMigrate {
			if err := users.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate platform users: %w", err)
			}
			if err := projects.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate platform projects: %w", err)
			}
			if err := platformruns.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate platform runs: %w", err)
			}
			if err := uploads.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate platform uploads: %w", err)
			}
			if err := novelstore.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate novel store: %w", err)
			}
			if err := approvals.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate platform approvals: %w", err)
			}
			if err := improvements.AutoMigrate(db); err != nil {
				return nil, fmt.Errorf("failed to migrate platform improvements: %w", err)
			}
		}
		if platformUserService == nil {
			platformUserService = users.NewService(db)
		}
		if platformProjectService == nil {
			platformProjectService = projects.NewService(db)
		}
		if platformRunService == nil {
			platformRunService = platformruns.NewService(db)
		}
		if platformUploadService == nil {
			platformUploadService = uploads.NewService(db, cfg.RuntimeConfig.Storage.Upload.Root)
		}
		if platformNovelStoreService == nil {
			obj, err := objectstore.FromRuntimeConfig(context.Background(), cfg.RuntimeConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to open novel object store: %w", err)
			}
			platformNovelStoreService = novelstore.NewService(db, obj)
		}
		if platformApprovalService == nil {
			platformApprovalService = approvals.NewService(db)
		}
		if platformImprovementService == nil {
			platformImprovementService = improvements.NewService(db)
		}
		if platformUserService != nil {
			if err := bootstrapConfiguredPrincipals(context.Background(), platformUserService, cfg.RuntimeConfig.Auth); err != nil {
				return nil, fmt.Errorf("failed to bootstrap platform principals: %w", err)
			}
		}
	}

	nativeSessionManager, err := newNativeSessionManager(cfg.RuntimeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create native sandbox session manager: %w", err)
	}

	router := mux.NewRouter().StrictSlash(true)
	// TODO: Allow taking a prefix to allow customizing the path
	// where the ADK REST API will be served.
	setupRouter(router,
		routers.NewAISphereAuthAPIRouter(controllers.NewAISphereAuthAPIController(cfg.RuntimeConfig)),
		routers.NewMeAPIRouter(controllers.NewMeAPIController(cfg.RuntimeConfig)),
		routers.NewPlatformUsersAPIRouter(controllers.NewPlatformUsersAPIController(platformUserService)),
		routers.NewPlatformProjectsAPIRouter(controllers.NewPlatformProjectsAPIController(platformProjectService, cfg.ArtifactService, cfg.SessionService, platformUploadService, platformNovelStoreService)),
		routers.NewPlatformUploadsAPIRouter(controllers.NewPlatformUploadsAPIController(platformUploadService, cfg.ArtifactService)),
		routers.NewPlatformNovelsAPIRouter(controllers.NewPlatformNovelsAPIController(platformNovelStoreService, platformUploadService)),
		routers.NewPlatformRunsAPIRouter(controllers.NewPlatformRunsAPIController(platformRunService)),
		routers.NewPlatformApprovalsAPIRouter(controllers.NewPlatformApprovalsAPIController(platformApprovalService)),
		routers.NewPlatformImprovementsAPIRouter(controllers.NewPlatformImprovementsAPIController(platformImprovementService)),
		routers.NewSessionsAPIRouter(controllers.NewSessionsAPIController(cfg.SessionService, subAgentObserveStore, nativeSessionManager)),
		routers.NewRuntimeAPIRouter(controllers.NewRuntimeAPIController(cfg.SessionService, cfg.MemoryService, cfg.AgentLoader, cfg.ArtifactService, cfg.SSEWriteTimeout, cfg.PluginConfig, false, cfg.RuntimeConfig, cfg.TraceRecorder, runStore, platformRunService, platformUploadService, subAgentObserveStore, nativeSessionManager)),
		routers.NewAppsAPIRouter(controllers.NewAppsAPIController(cfg.AgentLoader, cfg.BuilderAppsRoot, cfg.BuilderTmpRoot)),
		routers.NewMetadataAPIRouter(controllers.NewMetadataAPIController(cfg.RuntimeConfig)),
		routers.NewMCPAPIRouter(controllers.NewMCPAPIController(cfg.RuntimeConfig)),
		routers.NewSkillsAPIRouter(controllers.NewSkillsAPIController(cfg.SkillService)),
		routers.NewRuntimeTraceAPIRouter(controllers.NewRuntimeTraceAPIController(cfg.TraceRecorder)),
		routers.NewBuilderAPIRouter(controllers.NewBuilderAPIController(controllers.BuilderConfig{
			AppsRoot:     cfg.BuilderAppsRoot,
			TmpRoot:      cfg.BuilderTmpRoot,
			DefaultModel: cfg.BuilderDefaultModel,
		})),
		routers.NewDebugAPIRouter(controllers.NewDebugAPIController(cfg.SessionService, cfg.AgentLoader, debugTelemetry)),
		routers.NewArtifactsAPIRouter(controllers.NewArtifactsAPIController(cfg.ArtifactService)),
		&routers.EvalAPIRouter{},
	)
	if cfg.RuntimeConfig != nil {
		router.Use(auth.Middleware(cfg.RuntimeConfig.Auth))
	} else {
		router.Use(auth.Middleware(runtimeconfig.AuthConfig{Mode: "none"}))
	}
	return &Server{
		router:         router,
		telemetryStore: debugTelemetry,
	}, nil
}

func newNativeSessionManager(cfg *runtimeconfig.Config) (*sessionnative.Manager, error) {
	if cfg == nil {
		return nil, nil
	}
	sb := cfg.Skills.AIHub.Sandbox
	if !sb.Enabled || (!sb.NativeSession && !strings.EqualFold(strings.TrimSpace(sb.Mode), "agent-native")) {
		return nil, nil
	}
	if !sb.GoRunner {
		return nil, fmt.Errorf("skills.aihub.sandbox.go_runner must be true: the sandbox session-worker Agent loop has been removed from the production runtime path")
	}
	endpoint := strings.TrimSpace(sb.AdapterEndpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("skills.aihub.sandbox.adapter_endpoint is required for native sandbox sessions")
	}
	token := strings.TrimSpace(sb.AdapterToken)
	if token == "" && strings.TrimSpace(sb.AdapterTokenEnv) != "" {
		token = strings.TrimSpace(os.Getenv(strings.TrimSpace(sb.AdapterTokenEnv)))
	}
	var hubClient *aihubruntime.Client
	if cfg.Skills.AIHub.Enabled {
		client, err := aihubruntime.New(cfg.Skills.AIHub)
		if err != nil {
			return nil, err
		}
		hubClient = client
	}
	readyTimeout := time.Duration(sb.ReadyTimeoutSeconds) * time.Second
	if readyTimeout <= 0 {
		readyTimeout = 90 * time.Second
	}
	runtimeID := strings.TrimSpace(sb.RuntimeID)
	if runtimeID == "" {
		runtimeID = strings.TrimSpace(cfg.Skills.AIHub.RuntimeID)
	}
	if runtimeID == "" {
		runtimeID = "agentkit-runtime"
	}
	return &sessionnative.Manager{
		Sandbox:        sandboxclient.New(endpoint, token),
		Hub:            hubClient,
		RuntimeID:      runtimeID,
		SkillsRoot:     cfg.Skills.Root,
		DefaultProfile: sb.DefaultProfile,
		ReadyTimeout:   readyTimeout,
		GoRunner:       true,
		RuntimeConfig:  cfg,
	}, nil
}

func parseDurationDefault(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// ServerConfig contains parameters for the ADK REST API server.
type ServerConfig struct {
	SessionService  session.Service
	MemoryService   memory.Service
	AgentLoader     agent.Loader
	ArtifactService artifact.Service
	SSEWriteTimeout time.Duration
	PluginConfig    runner.PluginConfig
	DebugConfig     DebugTelemetryConfig

	BuilderAppsRoot            string
	BuilderTmpRoot             string
	BuilderDefaultModel        string
	RuntimeConfig              *runtimeconfig.Config
	TraceRecorder              runtimetrace.Recorder
	SkillService               skillservice.Service
	PlatformRunService         platformruns.Service
	PlatformApprovalService    approvals.Service
	PlatformImprovementService improvements.Service
	PlatformUserService        users.Service
	PlatformProjectService     projects.Service
	PlatformUploadService      uploads.Service
	PlatformNovelStoreService  *novelstore.Service
}

// DebugTelemetryConfig contains parameters for the debug telemetry.
type DebugTelemetryConfig struct {
	// Maximum number of traces to keep in memory.
	// If <= 0, the default capacity 10_000 is used.
	TraceCapacity int
}

// Server is an HTTP server that serves the ADK REST API.
type Server struct {
	router         *mux.Router
	telemetryStore *services.DebugTelemetry
}

// ServeHTTP makes [Server] implement [http.Handler] interface.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// SpanProcessor returns a processor that captures spans used for /debug/trace endpoint of the ADK REST API server.
// You can register it in your application TracerProvider to populate it with these spans.
func (s *Server) SpanProcessor() trace.SpanProcessor {
	return s.telemetryStore.SpanProcessor()
}

// LogProcessor returns a processor that captures log records used for /debug/trace endpoint of the ADK REST API server.
// You can register it in your application LoggerProvider to populate it with these logs.
func (s *Server) LogProcessor() sdklog.Processor {
	return s.telemetryStore.LogProcessor()
}

func bootstrapConfiguredPrincipals(ctx context.Context, svc users.Service, cfg runtimeconfig.AuthConfig) error {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" || mode == "none" {
		return svc.BootstrapPrincipal(ctx, "default", "admin", []string{"owner"})
	}
	if mode != "dev_token" {
		return nil
	}
	for _, token := range cfg.DevTokens {
		tenantID := token.TenantID
		if tenantID == "" {
			tenantID = "default"
		}
		userID := token.UserID
		if userID == "" {
			userID = "admin"
		}
		roles := token.Roles
		if len(roles) == 0 {
			roles = []string{"owner"}
		}
		if err := svc.BootstrapPrincipal(ctx, tenantID, userID, roles); err != nil {
			return err
		}
	}
	return nil
}

func setupRouter(router *mux.Router, subrouters ...routers.Router) *mux.Router {
	routers.SetupSubRouters(router, subrouters...)
	return router
}
