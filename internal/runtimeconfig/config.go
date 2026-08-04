// Copyright 2026 Google LLC
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

// Package runtimeconfig loads ADK runtime configuration from config files and
// environment variables. It intentionally sits below cmd/internal/adkcli and
// above concrete services so the launcher can decide implementations through
// configuration instead of hard-coded constructors.
package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"google.golang.org/genai"

	"google.golang.org/adk/artifact"
	"google.golang.org/adk/artifact/minioartifact"
	"google.golang.org/adk/internal/llminternal/googlellm"
	"google.golang.org/adk/internal/platform/pgutil"
	"google.golang.org/adk/memory"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/model/openai"
	"google.golang.org/adk/session"
	sessiondatabase "google.golang.org/adk/session/database"

	"github.com/glebarez/sqlite"
)

const (
	DefaultConfigName = "adk.yaml"
	EnvPrefix         = "ADK"
)

type contextKey string

const configContextKey contextKey = "runtimeconfig"

// Config is the top-level runtime config. It answers the questions:
//   - which service implementation should be used for each storage interface?
//   - which model spec should an agent's `model:` reference resolve to?
//   - what provider-specific options should be passed to the model adapter?
//   - what default generation/context limits should agents inherit?
type Config struct {
	ConfigPath string `mapstructure:"-" yaml:"-"`
	Root       string `mapstructure:"root" yaml:"root"`

	Server  ServerConfig  `mapstructure:"server" yaml:"server"`
	Auth    AuthConfig    `mapstructure:"auth" yaml:"auth"`
	Storage StorageConfig `mapstructure:"storage" yaml:"storage"`
	Models  ModelsConfig  `mapstructure:"models" yaml:"models"`
	Runtime RuntimeConfig `mapstructure:"runtime" yaml:"runtime"`
	Builder BuilderConfig `mapstructure:"builder" yaml:"builder"`
	Skills  SkillsConfig  `mapstructure:"skills" yaml:"skills"`
	MCP     MCPConfig     `mapstructure:"mcp" yaml:"mcp"`
}

// AuthConfig controls platform REST authentication. It is intentionally small
// for the first platformization stage; user/role management lives in
// internal/platform services.
type AuthConfig struct {
	Mode      string             `mapstructure:"mode" yaml:"mode"`
	DevTokens []DevTokenConfig   `mapstructure:"dev_tokens" yaml:"dev_tokens"`
	AISphere  AISphereAuthConfig `mapstructure:"aisphere" yaml:"aisphere"`
}

// AISphereAuthConfig configures validation of browser sessions issued by
// aisphere-auth. The service token stays server-side and is never sent to the
// browser.
type AISphereAuthConfig struct {
	Endpoint           string `mapstructure:"endpoint" yaml:"endpoint"`
	ServiceToken       string `mapstructure:"service_token" yaml:"service_token"`
	ServiceTokenEnv    string `mapstructure:"service_token_env" yaml:"service_token_env"`
	ServiceTokenHeader string `mapstructure:"service_token_header" yaml:"service_token_header"`
	CookieName         string `mapstructure:"cookie_name" yaml:"cookie_name"`
	App                string `mapstructure:"app" yaml:"app"`
	TimeoutSeconds     int    `mapstructure:"timeout_seconds" yaml:"timeout_seconds"`
}

type DevTokenConfig struct {
	Token    string   `mapstructure:"token" yaml:"token"`
	TokenEnv string   `mapstructure:"token_env" yaml:"token_env"`
	TenantID string   `mapstructure:"tenant_id" yaml:"tenant_id"`
	UserID   string   `mapstructure:"user_id" yaml:"user_id"`
	Roles    []string `mapstructure:"roles" yaml:"roles"`
	Scopes   []string `mapstructure:"scopes" yaml:"scopes"`
}

type ServerConfig struct {
	DefaultArgs []string      `mapstructure:"default_args" yaml:"default_args"`
	Web         WebConfig     `mapstructure:"web" yaml:"web"`
	API         APIConfig     `mapstructure:"api" yaml:"api"`
	WebUI       WebUIConfig   `mapstructure:"webui" yaml:"webui"`
	Console     ConsoleConfig `mapstructure:"console" yaml:"console"`
}

type WebConfig struct {
	Enabled         bool   `mapstructure:"enabled" yaml:"enabled"`
	Port            int    `mapstructure:"port" yaml:"port"`
	WriteTimeout    string `mapstructure:"write_timeout" yaml:"write_timeout"`
	ReadTimeout     string `mapstructure:"read_timeout" yaml:"read_timeout"`
	IdleTimeout     string `mapstructure:"idle_timeout" yaml:"idle_timeout"`
	ShutdownTimeout string `mapstructure:"shutdown_timeout" yaml:"shutdown_timeout"`
	OtelToCloud     bool   `mapstructure:"otel_to_cloud" yaml:"otel_to_cloud"`
}

type APIConfig struct {
	Enabled         bool                `mapstructure:"enabled" yaml:"enabled"`
	PathPrefix      string              `mapstructure:"path_prefix" yaml:"path_prefix"`
	FrontendAddress string              `mapstructure:"frontend_address" yaml:"frontend_address"`
	SSEWriteTimeout string              `mapstructure:"sse_write_timeout" yaml:"sse_write_timeout"`
	TraceCapacity   int                 `mapstructure:"trace_capacity" yaml:"trace_capacity"`
	ResumableRuns   ResumableRunsConfig `mapstructure:"resumable_runs" yaml:"resumable_runs"`
}

type ResumableRunsConfig struct {
	Enabled      bool     `mapstructure:"enabled" yaml:"enabled"`
	Mode         string   `mapstructure:"mode" yaml:"mode"`
	Addrs        []string `mapstructure:"addrs" yaml:"addrs"`
	Username     string   `mapstructure:"username" yaml:"username"`
	Password     string   `mapstructure:"password" yaml:"password"`
	PasswordEnv  string   `mapstructure:"password_env" yaml:"password_env"`
	DB           int      `mapstructure:"db" yaml:"db"`
	KeyPrefix    string   `mapstructure:"key_prefix" yaml:"key_prefix"`
	TTL          string   `mapstructure:"ttl" yaml:"ttl"`
	BlockTimeout string   `mapstructure:"block_timeout" yaml:"block_timeout"`
}

type WebUIConfig struct {
	Enabled          bool   `mapstructure:"enabled" yaml:"enabled"`
	APIServerAddress string `mapstructure:"api_server_address" yaml:"api_server_address"`
}

type ConsoleConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
}

type RuntimeConfig struct {
	ContextWindow int64                     `mapstructure:"context_window" yaml:"context_window"`
	Tracing       TracingConfig             `mapstructure:"tracing" yaml:"tracing"`
	InputPolicy   InputPolicyConfig         `mapstructure:"input_policy" yaml:"input_policy"`
	SubAgentTasks SubAgentTasksPolicyConfig `mapstructure:"subagent_tasks" yaml:"subagent_tasks"`
}

// SubAgentTasksPolicyConfig is enforced by the runtime task runner. Manager
// agents may request concurrency, but the effective value is clamped by this
// platform policy.
type SubAgentTasksPolicyConfig struct {
	DefaultConcurrency int    `mapstructure:"default_concurrency" yaml:"default_concurrency"`
	MaxConcurrency     int    `mapstructure:"max_concurrency" yaml:"max_concurrency"`
	ObserveTTL         string `mapstructure:"observe_ttl" yaml:"observe_ttl"`
}

// InputPolicyConfig protects the runtime from accidentally sending raw uploads
// or very large inline payloads directly to the model. Large files should go
// through Platform Uploads and tool/script preprocessing instead.
type InputPolicyConfig struct {
	RejectLargeInline   bool  `mapstructure:"reject_large_inline" yaml:"reject_large_inline"`
	MaxInlineTextChars  int   `mapstructure:"max_inline_text_chars" yaml:"max_inline_text_chars"`
	MaxInlineDataBytes  int64 `mapstructure:"max_inline_data_bytes" yaml:"max_inline_data_bytes"`
	WarnInlineTextChars int   `mapstructure:"warn_inline_text_chars" yaml:"warn_inline_text_chars"`
	WarnInlineDataBytes int64 `mapstructure:"warn_inline_data_bytes" yaml:"warn_inline_data_bytes"`
}

// TracingConfig controls platform-level runtime traces. It is observability-only;
// business callbacks remain the extension mechanism for changing behavior.
type TracingConfig struct {
	Enabled         bool   `mapstructure:"enabled" yaml:"enabled"`
	Root            string `mapstructure:"root" yaml:"root"`
	DumpLLMRequest  bool   `mapstructure:"dump_llm_request" yaml:"dump_llm_request"`
	DumpLLMResponse bool   `mapstructure:"dump_llm_response" yaml:"dump_llm_response"`
	DumpToolEvents  bool   `mapstructure:"dump_tool_events" yaml:"dump_tool_events"`
	DumpStream      bool   `mapstructure:"dump_stream_chunks" yaml:"dump_stream_chunks"`
	MaxContentChars int    `mapstructure:"max_content_chars" yaml:"max_content_chars"`
}

// BuilderConfig controls the WebUI builder filesystem workspace. The embedded
// ADK WebUI calls /builder/* endpoints to create/edit app yaml files.
type BuilderConfig struct {
	Enabled      bool   `mapstructure:"enabled" yaml:"enabled"`
	AppsRoot     string `mapstructure:"apps_root" yaml:"apps_root"`
	TmpRoot      string `mapstructure:"tmp_root" yaml:"tmp_root"`
	DefaultModel string `mapstructure:"default_model" yaml:"default_model"`
}

// SkillsConfig controls the filesystem skill repository. Skills are ADK
// SKILL.md folders exposed to agents through skilltoolset.
type SkillsConfig struct {
	Enabled bool             `mapstructure:"enabled" yaml:"enabled"`
	Root    string           `mapstructure:"root" yaml:"root"`
	Preload string           `mapstructure:"preload" yaml:"preload"`
	AIHub   AIHubSkillConfig `mapstructure:"aihub" yaml:"aihub"`
}

// AIHubSkillConfig controls permission-aware Skill synchronization from AIHub.
// It keeps Runtime as an execution plane: AIHub remains the catalog/control plane.
type AIHubSkillConfig struct {
	Enabled        bool               `mapstructure:"enabled" yaml:"enabled"`
	AgentMode      bool               `mapstructure:"agent_mode" yaml:"agent_mode"`
	Endpoint       string             `mapstructure:"endpoint" yaml:"endpoint"`
	SkillSet       string             `mapstructure:"skillset" yaml:"skillset"`
	SyncOnStart    bool               `mapstructure:"sync_on_start" yaml:"sync_on_start"`
	ReportOnStart  bool               `mapstructure:"report_on_start" yaml:"report_on_start"`
	RuntimeID      string             `mapstructure:"runtime_id" yaml:"runtime_id"`
	Token          string             `mapstructure:"token" yaml:"token"`
	TokenEnv       string             `mapstructure:"token_env" yaml:"token_env"`
	AuthHeader     string             `mapstructure:"auth_header" yaml:"auth_header"`
	AuthScheme     string             `mapstructure:"auth_scheme" yaml:"auth_scheme"`
	ExtraHeaders   map[string]string  `mapstructure:"extra_headers" yaml:"extra_headers"`
	TimeoutSeconds int                `mapstructure:"timeout_seconds" yaml:"timeout_seconds"`
	Watch          AIHubWatchConfig   `mapstructure:"watch" yaml:"watch"`
	Reload         AIHubReloadConfig  `mapstructure:"reload" yaml:"reload"`
	Revoke         AIHubRevokeConfig  `mapstructure:"revoke" yaml:"revoke"`
	Sandbox        AIHubSandboxConfig `mapstructure:"sandbox" yaml:"sandbox"`
}

type AIHubWatchConfig struct {
	Enabled             bool   `mapstructure:"enabled" yaml:"enabled"`
	Mode                string `mapstructure:"mode" yaml:"mode"`
	PollIntervalSeconds int    `mapstructure:"poll_interval_seconds" yaml:"poll_interval_seconds"`
	ReconnectSeconds    int    `mapstructure:"reconnect_seconds" yaml:"reconnect_seconds"`
}

type AIHubReloadConfig struct {
	Policy          string `mapstructure:"policy" yaml:"policy"` // new_sessions_only / immediate / manual
	KeepOldVersions int    `mapstructure:"keep_old_versions" yaml:"keep_old_versions"`
	VerifySHA256    bool   `mapstructure:"verify_sha256" yaml:"verify_sha256"`
}

type AIHubRevokeConfig struct {
	Policy       string `mapstructure:"policy" yaml:"policy"` // disable_new_sessions / cancel_running
	DeleteCached bool   `mapstructure:"delete_cached" yaml:"delete_cached"`
}

type AIHubSandboxConfig struct {
	Enabled            bool   `mapstructure:"enabled" yaml:"enabled"`
	Mode               string `mapstructure:"mode" yaml:"mode"` // local / docker / k8s / agent-native
	SessionsRoot       string `mapstructure:"sessions_root" yaml:"sessions_root"`
	ReadonlySkillMount bool   `mapstructure:"readonly_skill_mount" yaml:"readonly_skill_mount"`

	// Agent-native sandbox mode runs the session worker inside a sandbox Pod.
	// AgentKit keeps session/run metadata and proxies messages/events; the worker
	// owns the real agent execution environment under /workspace.
	NativeSession       bool   `mapstructure:"native_session" yaml:"native_session"`
	GoRunner            bool   `mapstructure:"go_runner" yaml:"go_runner"`
	AdapterEndpoint     string `mapstructure:"adapter_endpoint" yaml:"adapter_endpoint"`
	AdapterToken        string `mapstructure:"adapter_token" yaml:"adapter_token"`
	AdapterTokenEnv     string `mapstructure:"adapter_token_env" yaml:"adapter_token_env"`
	RuntimeID           string `mapstructure:"runtime_id" yaml:"runtime_id"`
	DefaultProfile      string `mapstructure:"default_profile" yaml:"default_profile"`
	ReadyTimeoutSeconds int    `mapstructure:"ready_timeout_seconds" yaml:"ready_timeout_seconds"`
	EventIdleSeconds    int    `mapstructure:"event_idle_seconds" yaml:"event_idle_seconds"`
}

// MCPConfig is the platform-level registry of MCP servers.
// Agent YAML should reference these servers by id instead of embedding endpoint
// and secret headers in every root_agent.yaml.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `mapstructure:"servers" yaml:"servers"`
}

// MCPServerConfig declares one MCP server connection. Secrets should usually be
// supplied through environment-expanded header values, for example:
// Authorization: "Bearer ${NOVEL_SPLITTER_MCP_TOKEN}".
type MCPServerConfig struct {
	Name                string            `mapstructure:"name" yaml:"name"`
	Description         string            `mapstructure:"description" yaml:"description"`
	Transport           string            `mapstructure:"transport" yaml:"transport"`
	Endpoint            string            `mapstructure:"endpoint" yaml:"endpoint"`
	Headers             map[string]string `mapstructure:"headers" yaml:"headers"`
	Namespace           string            `mapstructure:"namespace" yaml:"namespace"`
	Enabled             bool              `mapstructure:"enabled" yaml:"enabled"`
	TimeoutSeconds      int               `mapstructure:"timeout_seconds" yaml:"timeout_seconds"`
	ToolFilter          []string          `mapstructure:"tool_filter" yaml:"tool_filter"`
	RequireConfirmation bool              `mapstructure:"require_confirmation" yaml:"require_confirmation"`
}

type StorageConfig struct {
	Root     string         `mapstructure:"root" yaml:"root"`
	Database DatabaseConfig `mapstructure:"database" yaml:"database"`
	Session  ServiceConfig  `mapstructure:"session" yaml:"session"`
	Artifact ServiceConfig  `mapstructure:"artifact" yaml:"artifact"`
	Upload   ServiceConfig  `mapstructure:"upload" yaml:"upload"`
	Memory   ServiceConfig  `mapstructure:"memory" yaml:"memory"`
	KV       KVStoreConfig  `mapstructure:"kv" yaml:"kv"`
	Object   ObjectConfig   `mapstructure:"object" yaml:"object"`
	Extra    map[string]any `mapstructure:",remain" yaml:",inline"`
}

// DatabaseConfig is the shared relational database configuration used by
// platform-backed services. Individual services can either use it by setting
// type: database/db, or override Type/DSN/DSNEnv on their own ServiceConfig.
type DatabaseConfig struct {
	Type               string         `mapstructure:"type" yaml:"type"`
	Root               string         `mapstructure:"root" yaml:"root"`
	DSN                string         `mapstructure:"dsn" yaml:"dsn"`
	DSNEnv             string         `mapstructure:"dsn_env" yaml:"dsn_env"`
	AutoMigrate        bool           `mapstructure:"auto_migrate" yaml:"auto_migrate"`
	AutoCreateDatabase bool           `mapstructure:"auto_create_database" yaml:"auto_create_database"`
	MaintenanceDB      string         `mapstructure:"maintenance_database" yaml:"maintenance_database"`
	MaxOpenConns       int            `mapstructure:"max_open_conns" yaml:"max_open_conns"`
	MaxIdleConns       int            `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
	ConnMaxLifetime    string         `mapstructure:"conn_max_lifetime" yaml:"conn_max_lifetime"`
	ConnMaxIdleTime    string         `mapstructure:"conn_max_idle_time" yaml:"conn_max_idle_time"`
	Opts               map[string]any `mapstructure:"opts" yaml:"opts"`
}

type ServiceConfig struct {
	Type               string         `mapstructure:"type" yaml:"type"`
	Root               string         `mapstructure:"root" yaml:"root"`
	DSN                string         `mapstructure:"dsn" yaml:"dsn"`
	DSNEnv             string         `mapstructure:"dsn_env" yaml:"dsn_env"`
	Endpoint           string         `mapstructure:"endpoint" yaml:"endpoint"`
	Bucket             string         `mapstructure:"bucket" yaml:"bucket"`
	Region             string         `mapstructure:"region" yaml:"region"`
	AccessKey          string         `mapstructure:"access_key" yaml:"access_key"`
	AccessKeyEnv       string         `mapstructure:"access_key_env" yaml:"access_key_env"`
	SecretKey          string         `mapstructure:"secret_key" yaml:"secret_key"`
	SecretKeyEnv       string         `mapstructure:"secret_key_env" yaml:"secret_key_env"`
	UseSSL             bool           `mapstructure:"use_ssl" yaml:"use_ssl"`
	Prefix             string         `mapstructure:"prefix" yaml:"prefix"`
	CreateBucket       bool           `mapstructure:"create_bucket" yaml:"create_bucket"`
	PathStyle          bool           `mapstructure:"path_style" yaml:"path_style"`
	AutoMigrate        bool           `mapstructure:"auto_migrate" yaml:"auto_migrate"`
	AutoCreateDatabase bool           `mapstructure:"auto_create_database" yaml:"auto_create_database"`
	MaintenanceDB      string         `mapstructure:"maintenance_database" yaml:"maintenance_database"`
	MaxOpenConns       int            `mapstructure:"max_open_conns" yaml:"max_open_conns"`
	MaxIdleConns       int            `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
	ConnMaxLifetime    string         `mapstructure:"conn_max_lifetime" yaml:"conn_max_lifetime"`
	ConnMaxIdleTime    string         `mapstructure:"conn_max_idle_time" yaml:"conn_max_idle_time"`
	Opts               map[string]any `mapstructure:"opts" yaml:"opts"`
}

type KVStoreConfig struct {
	Type string         `mapstructure:"type" yaml:"type"`
	Root string         `mapstructure:"root" yaml:"root"`
	DSN  string         `mapstructure:"dsn" yaml:"dsn"`
	Opts map[string]any `mapstructure:"opts" yaml:"opts"`
}

type ObjectConfig struct {
	Type         string         `mapstructure:"type" yaml:"type"`
	Root         string         `mapstructure:"root" yaml:"root"`
	Endpoint     string         `mapstructure:"endpoint" yaml:"endpoint"`
	Bucket       string         `mapstructure:"bucket" yaml:"bucket"`
	Region       string         `mapstructure:"region" yaml:"region"`
	AccessKey    string         `mapstructure:"access_key" yaml:"access_key"`
	AccessKeyEnv string         `mapstructure:"access_key_env" yaml:"access_key_env"`
	SecretKey    string         `mapstructure:"secret_key" yaml:"secret_key"`
	SecretKeyEnv string         `mapstructure:"secret_key_env" yaml:"secret_key_env"`
	UseSSL       bool           `mapstructure:"use_ssl" yaml:"use_ssl"`
	Prefix       string         `mapstructure:"prefix" yaml:"prefix"`
	CreateBucket bool           `mapstructure:"create_bucket" yaml:"create_bucket"`
	PathStyle    bool           `mapstructure:"path_style" yaml:"path_style"`
	Opts         map[string]any `mapstructure:"opts" yaml:"opts"`
}

type ModelsConfig struct {
	Default string               `mapstructure:"default" yaml:"default"`
	Specs   map[string]ModelSpec `mapstructure:"specs" yaml:"specs"`
	Aliases map[string]string    `mapstructure:"aliases" yaml:"aliases"`
	Extra   map[string]any       `mapstructure:",remain" yaml:",inline"`
}

type ModelSpec struct {
	Provider      string            `mapstructure:"provider" yaml:"provider"`
	Model         string            `mapstructure:"model" yaml:"model"`
	BaseURL       string            `mapstructure:"base_url" yaml:"base_url"`
	APIKey        string            `mapstructure:"api_key" yaml:"api_key"`
	APIKeyEnv     string            `mapstructure:"api_key_env" yaml:"api_key_env"`
	Headers       map[string]string `mapstructure:"headers" yaml:"headers"`
	StrictTools   bool              `mapstructure:"strict_tools" yaml:"strict_tools"`
	ContextWindow int64             `mapstructure:"context_window" yaml:"context_window"`
	Generation    GenerationConfig  `mapstructure:"generation" yaml:"generation"`
	ExtraBody     map[string]any    `mapstructure:"extra_body" yaml:"extra_body"`
	Timeout       string            `mapstructure:"timeout" yaml:"timeout"`
	Extra         map[string]any    `mapstructure:",remain" yaml:",inline"`
}

type GenerationConfig struct {
	Temperature      *float32       `mapstructure:"temperature" yaml:"temperature"`
	TopP             *float32       `mapstructure:"top_p" yaml:"top_p"`
	TopK             *float32       `mapstructure:"top_k" yaml:"top_k"`
	MaxOutputTokens  *int32         `mapstructure:"max_output_tokens" yaml:"max_output_tokens"`
	CandidateCount   *int32         `mapstructure:"candidate_count" yaml:"candidate_count"`
	StopSequences    []string       `mapstructure:"stop_sequences" yaml:"stop_sequences"`
	ResponseMIMEType string         `mapstructure:"response_mime_type" yaml:"response_mime_type"`
	ExtraBody        map[string]any `mapstructure:"extra_body" yaml:"extra_body"`
}

// Services contains concrete service implementations selected by Config.
type Services struct {
	Session  session.Service
	Artifact artifact.Service
	Memory   memory.Service
}

// Default returns a local-development-friendly config. Production deployments
// should usually override this through adk.yaml and environment variables.
func Default(cwd string) *Config {
	root := filepath.Join(cwd, ".adk")
	dataRoot := filepath.Join(root, "data")
	return &Config{
		Root: root,
		Server: ServerConfig{
			Web: WebConfig{
				Enabled:         true,
				Port:            8080,
				WriteTimeout:    "15s",
				ReadTimeout:     "15s",
				IdleTimeout:     "60s",
				ShutdownTimeout: "15s",
			},
			API: APIConfig{
				Enabled:         true,
				PathPrefix:      "/api",
				FrontendAddress: "http://localhost:8080",
				SSEWriteTimeout: "120s",
				TraceCapacity:   10000,
				ResumableRuns: ResumableRunsConfig{
					Enabled:      false,
					Mode:         "standalone",
					KeyPrefix:    "adk:run",
					TTL:          "6h",
					BlockTimeout: "15s",
				},
			},
		},
		Auth: AuthConfig{
			Mode: "none",
		},
		Storage: StorageConfig{
			Root:     dataRoot,
			Database: DatabaseConfig{Type: "sqlite", Root: filepath.Join(dataRoot, "database"), DSN: filepath.Join(dataRoot, "database", "adk.db"), AutoMigrate: true},
			Session:  ServiceConfig{Type: "filesystem", Root: filepath.Join(dataRoot, "sessions")},
			Artifact: ServiceConfig{Type: "filesystem", Root: filepath.Join(dataRoot, "artifacts")},
			Upload:   ServiceConfig{Type: "filesystem", Root: filepath.Join(dataRoot, "uploads")},
			Memory:   ServiceConfig{Type: "filesystem", Root: filepath.Join(dataRoot, "memory")},
			KV:       KVStoreConfig{Type: "filesystem", Root: filepath.Join(dataRoot, "kv")},
			Object:   ObjectConfig{Type: "filesystem", Root: filepath.Join(dataRoot, "objects")},
		},
		Models: ModelsConfig{
			Default: "default",
			Aliases: map[string]string{
				"default": "openai_default",
			},
			Specs: map[string]ModelSpec{
				"openai_default": {
					Provider:      "openai",
					Model:         firstNonEmpty(os.Getenv("OPENAI_COMPAT_MODEL"), os.Getenv("OPENAI_MODEL"), "gpt-4.1-mini"),
					BaseURL:       firstNonEmpty(os.Getenv("OPENAI_COMPAT_BASE_URL"), os.Getenv("OPENAI_BASE_URL"), "https://api.openai.com/v1"),
					APIKeyEnv:     firstNonEmpty(os.Getenv("OPENAI_COMPAT_API_KEY_ENV"), "OPENAI_COMPAT_API_KEY"),
					ContextWindow: 128000,
					Generation: GenerationConfig{
						MaxOutputTokens: int32Ptr(8192),
					},
				},
			},
		},
		Runtime: RuntimeConfig{
			ContextWindow: 128000,
			SubAgentTasks: SubAgentTasksPolicyConfig{
				DefaultConcurrency: 5,
				MaxConcurrency:     5,
				ObserveTTL:         "6h",
			},
			InputPolicy: InputPolicyConfig{
				RejectLargeInline:   true,
				MaxInlineTextChars:  64_000,
				MaxInlineDataBytes:  256 << 10,
				WarnInlineTextChars: 16_000,
				WarnInlineDataBytes: 64 << 10,
			},
			Tracing: TracingConfig{
				Enabled:         true,
				Root:            filepath.Join(dataRoot, "traces"),
				DumpLLMRequest:  true,
				DumpLLMResponse: true,
				DumpToolEvents:  true,
				DumpStream:      false,
				MaxContentChars: 8000,
			},
		},
		Builder: BuilderConfig{
			Enabled:      true,
			AppsRoot:     filepath.Join(cwd, "agents"),
			TmpRoot:      filepath.Join(root, "builder", "tmp"),
			DefaultModel: "default",
		},
		Skills: SkillsConfig{
			Enabled: true,
			Root:    filepath.Join(cwd, "skills"),
			Preload: "complete",
		},
	}
}

// Load reads adk.yaml/config.yaml plus environment overrides. explicitPath may
// be empty; in that case ADK_CONFIG is honored, then common project locations.
func Load(cwd, explicitPath string) (*Config, error) {
	cfg := Default(cwd)
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	// Register defaults so env-only configuration also works.
	setDefaults(v, cfg)

	if explicitPath == "" {
		explicitPath = os.Getenv("ADK_CONFIG")
	}
	if err := readConfig(v, cwd, explicitPath); err != nil {
		return nil, err
	}
	cfg.ConfigPath = v.ConfigFileUsed()

	// Use Viper's default mapstructure decoder options. Viper already uses
	// mapstructure tags and includes string-to-duration/string-to-slice hooks.
	// Avoid importing a concrete mapstructure package here: Viper v1.20+ uses
	// github.com/go-viper/mapstructure/v2 internally, while older examples often
	// import github.com/mitchellh/mapstructure; mixing the two causes a compile
	// time type mismatch for DecoderConfigOption.
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("decode runtime config: %w", err)
	}
	cfg.normalize(cwd)
	return cfg, nil
}

func readConfig(v *viper.Viper, cwd, explicitPath string) error {
	if explicitPath != "" {
		v.SetConfigFile(explicitPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read runtime config %q: %w", explicitPath, err)
		}
		return nil
	}

	searchPaths := []string{cwd, filepath.Join(cwd, "config"), filepath.Join(cwd, ".adk")}
	for _, p := range searchPaths {
		v.AddConfigPath(p)
	}
	for _, name := range []string{"adk", "config"} {
		v.SetConfigName(name)
		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if errors.As(err, &notFound) {
				continue
			}
			return fmt.Errorf("read runtime config: %w", err)
		}
		return nil
	}
	return nil
}

func setDefaults(v *viper.Viper, cfg *Config) {
	v.SetDefault("root", cfg.Root)
	v.SetDefault("server.web.enabled", cfg.Server.Web.Enabled)
	v.SetDefault("server.web.port", cfg.Server.Web.Port)
	v.SetDefault("server.web.write_timeout", cfg.Server.Web.WriteTimeout)
	v.SetDefault("server.web.read_timeout", cfg.Server.Web.ReadTimeout)
	v.SetDefault("server.web.idle_timeout", cfg.Server.Web.IdleTimeout)
	v.SetDefault("server.web.shutdown_timeout", cfg.Server.Web.ShutdownTimeout)
	v.SetDefault("server.api.enabled", cfg.Server.API.Enabled)
	v.SetDefault("server.api.path_prefix", cfg.Server.API.PathPrefix)
	v.SetDefault("server.api.frontend_address", cfg.Server.API.FrontendAddress)
	v.SetDefault("server.api.sse_write_timeout", cfg.Server.API.SSEWriteTimeout)
	v.SetDefault("server.api.trace_capacity", cfg.Server.API.TraceCapacity)
	v.SetDefault("server.api.resumable_runs.enabled", cfg.Server.API.ResumableRuns.Enabled)
	v.SetDefault("server.api.resumable_runs.mode", cfg.Server.API.ResumableRuns.Mode)
	v.SetDefault("server.api.resumable_runs.key_prefix", cfg.Server.API.ResumableRuns.KeyPrefix)
	v.SetDefault("server.api.resumable_runs.ttl", cfg.Server.API.ResumableRuns.TTL)
	v.SetDefault("server.api.resumable_runs.block_timeout", cfg.Server.API.ResumableRuns.BlockTimeout)
	v.SetDefault("auth.mode", cfg.Auth.Mode)
	v.SetDefault("auth.aisphere.endpoint", cfg.Auth.AISphere.Endpoint)
	v.SetDefault("auth.aisphere.service_token", cfg.Auth.AISphere.ServiceToken)
	v.SetDefault("auth.aisphere.service_token_env", cfg.Auth.AISphere.ServiceTokenEnv)
	v.SetDefault("auth.aisphere.service_token_header", cfg.Auth.AISphere.ServiceTokenHeader)
	v.SetDefault("auth.aisphere.cookie_name", cfg.Auth.AISphere.CookieName)
	v.SetDefault("auth.aisphere.app", cfg.Auth.AISphere.App)
	v.SetDefault("auth.aisphere.timeout_seconds", cfg.Auth.AISphere.TimeoutSeconds)
	v.SetDefault("storage.root", cfg.Storage.Root)
	v.SetDefault("storage.database.type", cfg.Storage.Database.Type)
	v.SetDefault("storage.database.root", cfg.Storage.Database.Root)
	v.SetDefault("storage.database.dsn", cfg.Storage.Database.DSN)
	v.SetDefault("storage.database.dsn_env", cfg.Storage.Database.DSNEnv)
	v.SetDefault("storage.database.auto_migrate", cfg.Storage.Database.AutoMigrate)
	v.SetDefault("storage.database.auto_create_database", cfg.Storage.Database.AutoCreateDatabase)
	v.SetDefault("storage.database.maintenance_database", cfg.Storage.Database.MaintenanceDB)
	v.SetDefault("storage.database.max_open_conns", cfg.Storage.Database.MaxOpenConns)
	v.SetDefault("storage.database.max_idle_conns", cfg.Storage.Database.MaxIdleConns)
	v.SetDefault("storage.database.conn_max_lifetime", cfg.Storage.Database.ConnMaxLifetime)
	v.SetDefault("storage.database.conn_max_idle_time", cfg.Storage.Database.ConnMaxIdleTime)
	v.SetDefault("storage.session.type", cfg.Storage.Session.Type)
	v.SetDefault("storage.session.root", cfg.Storage.Session.Root)
	v.SetDefault("storage.session.dsn", cfg.Storage.Session.DSN)
	v.SetDefault("storage.session.dsn_env", cfg.Storage.Session.DSNEnv)
	v.SetDefault("storage.session.auto_migrate", cfg.Storage.Session.AutoMigrate)
	v.SetDefault("storage.session.auto_create_database", cfg.Storage.Session.AutoCreateDatabase)
	v.SetDefault("storage.session.maintenance_database", cfg.Storage.Session.MaintenanceDB)
	v.SetDefault("storage.session.max_open_conns", cfg.Storage.Session.MaxOpenConns)
	v.SetDefault("storage.session.max_idle_conns", cfg.Storage.Session.MaxIdleConns)
	v.SetDefault("storage.session.conn_max_lifetime", cfg.Storage.Session.ConnMaxLifetime)
	v.SetDefault("storage.session.conn_max_idle_time", cfg.Storage.Session.ConnMaxIdleTime)
	v.SetDefault("storage.artifact.type", cfg.Storage.Artifact.Type)
	v.SetDefault("storage.artifact.root", cfg.Storage.Artifact.Root)
	v.SetDefault("storage.upload.type", cfg.Storage.Upload.Type)
	v.SetDefault("storage.upload.root", cfg.Storage.Upload.Root)
	v.SetDefault("storage.memory.type", cfg.Storage.Memory.Type)
	v.SetDefault("storage.memory.root", cfg.Storage.Memory.Root)
	v.SetDefault("storage.object.type", cfg.Storage.Object.Type)
	v.SetDefault("storage.object.root", cfg.Storage.Object.Root)
	v.SetDefault("storage.object.endpoint", cfg.Storage.Object.Endpoint)
	v.SetDefault("storage.object.bucket", cfg.Storage.Object.Bucket)
	v.SetDefault("storage.object.region", cfg.Storage.Object.Region)
	v.SetDefault("storage.object.access_key", cfg.Storage.Object.AccessKey)
	v.SetDefault("storage.object.access_key_env", cfg.Storage.Object.AccessKeyEnv)
	v.SetDefault("storage.object.secret_key", cfg.Storage.Object.SecretKey)
	v.SetDefault("storage.object.secret_key_env", cfg.Storage.Object.SecretKeyEnv)
	v.SetDefault("storage.object.use_ssl", cfg.Storage.Object.UseSSL)
	v.SetDefault("storage.object.prefix", cfg.Storage.Object.Prefix)
	v.SetDefault("storage.object.create_bucket", cfg.Storage.Object.CreateBucket)
	v.SetDefault("storage.object.path_style", cfg.Storage.Object.PathStyle)
	v.SetDefault("models.default", cfg.Models.Default)
	v.SetDefault("runtime.context_window", cfg.Runtime.ContextWindow)
	v.SetDefault("runtime.input_policy.reject_large_inline", cfg.Runtime.InputPolicy.RejectLargeInline)
	v.SetDefault("runtime.input_policy.max_inline_text_chars", cfg.Runtime.InputPolicy.MaxInlineTextChars)
	v.SetDefault("runtime.input_policy.max_inline_data_bytes", cfg.Runtime.InputPolicy.MaxInlineDataBytes)
	v.SetDefault("runtime.input_policy.warn_inline_text_chars", cfg.Runtime.InputPolicy.WarnInlineTextChars)
	v.SetDefault("runtime.input_policy.warn_inline_data_bytes", cfg.Runtime.InputPolicy.WarnInlineDataBytes)
	v.SetDefault("runtime.subagent_tasks.default_concurrency", cfg.Runtime.SubAgentTasks.DefaultConcurrency)
	v.SetDefault("runtime.subagent_tasks.max_concurrency", cfg.Runtime.SubAgentTasks.MaxConcurrency)
	v.SetDefault("runtime.subagent_tasks.observe_ttl", cfg.Runtime.SubAgentTasks.ObserveTTL)
	v.SetDefault("runtime.tracing.enabled", cfg.Runtime.Tracing.Enabled)
	v.SetDefault("runtime.tracing.root", cfg.Runtime.Tracing.Root)
	v.SetDefault("runtime.tracing.dump_llm_request", cfg.Runtime.Tracing.DumpLLMRequest)
	v.SetDefault("runtime.tracing.dump_llm_response", cfg.Runtime.Tracing.DumpLLMResponse)
	v.SetDefault("runtime.tracing.dump_tool_events", cfg.Runtime.Tracing.DumpToolEvents)
	v.SetDefault("runtime.tracing.dump_stream_chunks", cfg.Runtime.Tracing.DumpStream)
	v.SetDefault("runtime.tracing.max_content_chars", cfg.Runtime.Tracing.MaxContentChars)
	v.SetDefault("builder.enabled", cfg.Builder.Enabled)
	v.SetDefault("builder.apps_root", cfg.Builder.AppsRoot)
	v.SetDefault("builder.tmp_root", cfg.Builder.TmpRoot)
	v.SetDefault("builder.default_model", cfg.Builder.DefaultModel)
	v.SetDefault("skills.enabled", cfg.Skills.Enabled)
	v.SetDefault("skills.root", cfg.Skills.Root)
	v.SetDefault("skills.preload", cfg.Skills.Preload)
	v.SetDefault("skills.aihub.enabled", cfg.Skills.AIHub.Enabled)
	v.SetDefault("skills.aihub.agent_mode", cfg.Skills.AIHub.AgentMode)
	v.SetDefault("skills.aihub.endpoint", cfg.Skills.AIHub.Endpoint)
	v.SetDefault("skills.aihub.skillset", cfg.Skills.AIHub.SkillSet)
	v.SetDefault("skills.aihub.sync_on_start", cfg.Skills.AIHub.SyncOnStart)
	v.SetDefault("skills.aihub.report_on_start", cfg.Skills.AIHub.ReportOnStart)
	v.SetDefault("skills.aihub.runtime_id", cfg.Skills.AIHub.RuntimeID)
	v.SetDefault("skills.aihub.token", cfg.Skills.AIHub.Token)
	v.SetDefault("skills.aihub.token_env", cfg.Skills.AIHub.TokenEnv)
	v.SetDefault("skills.aihub.auth_header", cfg.Skills.AIHub.AuthHeader)
	v.SetDefault("skills.aihub.auth_scheme", cfg.Skills.AIHub.AuthScheme)
	v.SetDefault("skills.aihub.timeout_seconds", cfg.Skills.AIHub.TimeoutSeconds)
	v.SetDefault("skills.aihub.watch.enabled", cfg.Skills.AIHub.Watch.Enabled)
	v.SetDefault("skills.aihub.watch.mode", cfg.Skills.AIHub.Watch.Mode)
	v.SetDefault("skills.aihub.watch.poll_interval_seconds", cfg.Skills.AIHub.Watch.PollIntervalSeconds)
	v.SetDefault("skills.aihub.watch.reconnect_seconds", cfg.Skills.AIHub.Watch.ReconnectSeconds)
	v.SetDefault("skills.aihub.reload.policy", cfg.Skills.AIHub.Reload.Policy)
	v.SetDefault("skills.aihub.reload.keep_old_versions", cfg.Skills.AIHub.Reload.KeepOldVersions)
	v.SetDefault("skills.aihub.reload.verify_sha256", cfg.Skills.AIHub.Reload.VerifySHA256)
	v.SetDefault("skills.aihub.revoke.policy", cfg.Skills.AIHub.Revoke.Policy)
	v.SetDefault("skills.aihub.revoke.delete_cached", cfg.Skills.AIHub.Revoke.DeleteCached)
	v.SetDefault("skills.aihub.sandbox.enabled", cfg.Skills.AIHub.Sandbox.Enabled)
	v.SetDefault("skills.aihub.sandbox.mode", cfg.Skills.AIHub.Sandbox.Mode)
	v.SetDefault("skills.aihub.sandbox.sessions_root", cfg.Skills.AIHub.Sandbox.SessionsRoot)
	v.SetDefault("skills.aihub.sandbox.readonly_skill_mount", cfg.Skills.AIHub.Sandbox.ReadonlySkillMount)
	v.SetDefault("skills.aihub.sandbox.go_runner", cfg.Skills.AIHub.Sandbox.GoRunner)
}

func (c *Config) normalize(cwd string) {
	if c.Root == "" {
		c.Root = filepath.Join(cwd, ".adk")
	}
	c.Root = absPath(cwd, c.Root)
	if c.Storage.Root == "" {
		c.Storage.Root = filepath.Join(c.Root, "data")
	}
	c.Storage.Root = absPath(cwd, c.Storage.Root)

	if c.Auth.Mode == "" {
		c.Auth.Mode = "none"
	}

	if c.Storage.Database.Type == "" {
		c.Storage.Database.Type = "sqlite"
	}
	if c.Storage.Database.Root == "" {
		c.Storage.Database.Root = filepath.Join(c.Storage.Root, "database")
	}
	if c.Storage.Database.DSN == "" && c.Storage.Database.DSNEnv != "" {
		c.Storage.Database.DSN = os.Getenv(c.Storage.Database.DSNEnv)
	}
	if c.Storage.Database.DSN == "" && normalizeType(c.Storage.Database.Type) == "sqlite" {
		c.Storage.Database.DSN = filepath.Join(c.Storage.Database.Root, "adk.db")
	}
	if c.Storage.Session.Type == "" {
		c.Storage.Session.Type = "filesystem"
	}
	if c.Storage.Artifact.Type == "" {
		c.Storage.Artifact.Type = "filesystem"
	}
	if c.Storage.Upload.Type == "" {
		c.Storage.Upload.Type = "filesystem"
	}
	if c.Storage.Memory.Type == "" {
		c.Storage.Memory.Type = "filesystem"
	}
	if c.Storage.Session.Root == "" {
		c.Storage.Session.Root = filepath.Join(c.Storage.Root, "sessions")
	}
	if c.Storage.Artifact.Root == "" {
		c.Storage.Artifact.Root = filepath.Join(c.Storage.Root, "artifacts")
	}
	if c.Storage.Upload.Root == "" {
		c.Storage.Upload.Root = filepath.Join(c.Storage.Root, "uploads")
	}
	if c.Storage.Memory.Root == "" {
		c.Storage.Memory.Root = filepath.Join(c.Storage.Root, "memory")
	}
	c.Storage.Database.Root = absPath(cwd, c.Storage.Database.Root)
	if normalizeType(c.Storage.Database.Type) == "sqlite" && c.Storage.Database.DSN != "" && !looksLikeMemorySQLiteDSN(c.Storage.Database.DSN) {
		c.Storage.Database.DSN = absPath(cwd, c.Storage.Database.DSN)
	}
	if c.Storage.Session.DSN == "" && c.Storage.Session.DSNEnv != "" {
		c.Storage.Session.DSN = os.Getenv(c.Storage.Session.DSNEnv)
	}
	c.Storage.Session.Root = absPath(cwd, c.Storage.Session.Root)
	if normalizeType(c.Storage.Session.Type) == "sqlite" && c.Storage.Session.DSN != "" && !looksLikeMemorySQLiteDSN(c.Storage.Session.DSN) {
		c.Storage.Session.DSN = absPath(cwd, c.Storage.Session.DSN)
	}
	c.Storage.Artifact.Root = absPath(cwd, c.Storage.Artifact.Root)
	c.Storage.Upload.Root = absPath(cwd, c.Storage.Upload.Root)
	c.Storage.Memory.Root = absPath(cwd, c.Storage.Memory.Root)
	if c.Storage.KV.Root != "" {
		c.Storage.KV.Root = absPath(cwd, c.Storage.KV.Root)
	}
	if c.Storage.Object.Root != "" {
		c.Storage.Object.Root = absPath(cwd, c.Storage.Object.Root)
	}
	if c.Storage.Object.AccessKey == "" && c.Storage.Object.AccessKeyEnv != "" {
		c.Storage.Object.AccessKey = os.Getenv(c.Storage.Object.AccessKeyEnv)
	}
	if c.Storage.Object.SecretKey == "" && c.Storage.Object.SecretKeyEnv != "" {
		c.Storage.Object.SecretKey = os.Getenv(c.Storage.Object.SecretKeyEnv)
	}

	if c.Models.Specs == nil {
		c.Models.Specs = map[string]ModelSpec{}
	}
	if c.Models.Aliases == nil {
		c.Models.Aliases = map[string]string{}
	}
	if c.Models.Default == "" {
		c.Models.Default = "default"
	}
	if _, ok := c.Models.Specs["openai_default"]; !ok {
		c.Models.Specs["openai_default"] = Default(cwd).Models.Specs["openai_default"]
	}
	if _, ok := c.Models.Aliases["default"]; !ok {
		c.Models.Aliases["default"] = "openai_default"
	}

	if c.Runtime.InputPolicy.MaxInlineTextChars <= 0 {
		c.Runtime.InputPolicy.MaxInlineTextChars = 64000
	}
	if c.Runtime.InputPolicy.MaxInlineDataBytes <= 0 {
		c.Runtime.InputPolicy.MaxInlineDataBytes = 256 << 10
	}
	if c.Runtime.InputPolicy.WarnInlineTextChars <= 0 {
		c.Runtime.InputPolicy.WarnInlineTextChars = 16000
	}
	if c.Runtime.InputPolicy.WarnInlineDataBytes <= 0 {
		c.Runtime.InputPolicy.WarnInlineDataBytes = 64 << 10
	}

	if c.Runtime.Tracing.Root == "" {
		c.Runtime.Tracing.Root = filepath.Join(c.Storage.Root, "traces")
	}
	c.Runtime.Tracing.Root = absPath(cwd, c.Runtime.Tracing.Root)
	if c.Runtime.Tracing.MaxContentChars <= 0 {
		c.Runtime.Tracing.MaxContentChars = 8000
	}

	if c.Builder.AppsRoot == "" {
		c.Builder.AppsRoot = filepath.Join(cwd, "agents")
	}
	if c.Builder.TmpRoot == "" {
		c.Builder.TmpRoot = filepath.Join(c.Root, "builder", "tmp")
	}
	if c.Builder.DefaultModel == "" {
		c.Builder.DefaultModel = c.Models.Default
	}
	c.Builder.AppsRoot = absPath(cwd, c.Builder.AppsRoot)
	c.Builder.TmpRoot = absPath(cwd, c.Builder.TmpRoot)

	if c.Skills.Root == "" {
		c.Skills.Root = filepath.Join(cwd, "skills")
	}
	if c.Auth.AISphere.ServiceToken == "" && c.Auth.AISphere.ServiceTokenEnv != "" {
		c.Auth.AISphere.ServiceToken = os.Getenv(c.Auth.AISphere.ServiceTokenEnv)
	}
	if c.Auth.AISphere.CookieName == "" {
		c.Auth.AISphere.CookieName = "aisphere_session"
	}
	if c.Auth.AISphere.App == "" {
		c.Auth.AISphere.App = "agentkit"
	}
	if c.Auth.AISphere.TimeoutSeconds <= 0 {
		c.Auth.AISphere.TimeoutSeconds = 10
	}
	if c.Skills.Preload == "" {
		c.Skills.Preload = "complete"
	}
	c.Skills.Root = absPath(cwd, c.Skills.Root)
	if c.Skills.AIHub.Endpoint == "" {
		c.Skills.AIHub.Endpoint = os.Getenv("AIHUB_ENDPOINT")
	}
	if c.Skills.AIHub.Token == "" && c.Skills.AIHub.TokenEnv != "" {
		c.Skills.AIHub.Token = os.Getenv(c.Skills.AIHub.TokenEnv)
	}
	if c.Skills.AIHub.Token == "" {
		c.Skills.AIHub.Token = os.Getenv("AIHUB_RUNTIME_TOKEN")
	}
	if c.Skills.AIHub.AuthHeader == "" {
		c.Skills.AIHub.AuthHeader = "Authorization"
	}
	if c.Skills.AIHub.AuthScheme == "" && strings.EqualFold(c.Skills.AIHub.AuthHeader, "Authorization") {
		c.Skills.AIHub.AuthScheme = "Bearer"
	}
	if c.Skills.AIHub.RuntimeID == "" {
		c.Skills.AIHub.RuntimeID = os.Getenv("AIHUB_RUNTIME_ID")
	}
	if c.Skills.AIHub.TimeoutSeconds <= 0 {
		c.Skills.AIHub.TimeoutSeconds = 30
	}
	if c.Skills.AIHub.ExtraHeaders == nil {
		c.Skills.AIHub.ExtraHeaders = map[string]string{}
	}

	if c.MCP.Servers == nil {
		c.MCP.Servers = map[string]MCPServerConfig{}
	}
	for id, server := range c.MCP.Servers {
		if server.Transport == "" {
			server.Transport = "streamable_http"
		}
		if server.Name == "" {
			server.Name = id
		}
		if server.Namespace == "" {
			server.Namespace = id
		}
		if server.TimeoutSeconds <= 0 {
			server.TimeoutSeconds = 60
		}
		if server.Headers == nil {
			server.Headers = map[string]string{}
		}
		c.MCP.Servers[id] = server
	}
}

// WithConfig stores runtime config in context for code paths such as
// configurable.FromConfig.
func WithConfig(ctx context.Context, cfg *Config) context.Context {
	if cfg == nil {
		return ctx
	}
	return context.WithValue(ctx, configContextKey, cfg)
}

// FromContext returns runtime config attached to ctx. If absent, a safe default
// is returned using current working directory as root.
func FromContext(ctx context.Context) *Config {
	if ctx != nil {
		if cfg, ok := ctx.Value(configContextKey).(*Config); ok && cfg != nil {
			return cfg
		}
	}
	cwd, _ := os.Getwd()
	return Default(cwd)
}

// MCPServer resolves one configured MCP server by id.
func (c *Config) MCPServer(id string) (MCPServerConfig, bool) {
	if c == nil || c.MCP.Servers == nil {
		return MCPServerConfig{}, false
	}
	server, ok := c.MCP.Servers[id]
	return server, ok
}

func (c *Config) BuildServices() (*Services, error) {
	ss, err := buildSessionService(c.Storage.Session, c.Storage.Database)
	if err != nil {
		return nil, err
	}
	as, err := buildArtifactService(c.Storage.Artifact)
	if err != nil {
		return nil, err
	}
	ms, err := buildMemoryService(c.Storage.Memory)
	if err != nil {
		return nil, err
	}
	return &Services{Session: ss, Artifact: as, Memory: ms}, nil
}

func buildSessionService(cfg ServiceConfig, dbCfg DatabaseConfig) (session.Service, error) {
	switch normalizeType(cfg.Type) {
	case "", "filesystem", "file", "fs", "localfs":
		return session.FileSystemService(cfg.Root)
	case "memory", "inmemory", "in_memory":
		return session.InMemoryService(), nil
	case "database", "db", "sql", "relational", "sqlite", "sqlite3", "postgres", "postgresql", "mysql":
		return buildDatabaseSessionService(cfg, dbCfg)
	default:
		return nil, fmt.Errorf("unsupported session service type %q", cfg.Type)
	}
}

func buildDatabaseSessionService(cfg ServiceConfig, dbCfg DatabaseConfig) (session.Service, error) {
	dbType := normalizeType(cfg.Type)
	if dbType == "" || dbType == "database" || dbType == "db" || dbType == "sql" || dbType == "relational" {
		dbType = normalizeType(dbCfg.Type)
	}
	if dbType == "" {
		dbType = "sqlite"
	}

	dsn := firstNonEmpty(cfg.DSN, envValue(cfg.DSNEnv), dbCfg.DSN, envValue(dbCfg.DSNEnv))
	autoMigrate := cfg.AutoMigrate || dbCfg.AutoMigrate
	autoCreateDatabase := cfg.AutoCreateDatabase || dbCfg.AutoCreateDatabase
	maintenanceDB := firstNonEmpty(cfg.MaintenanceDB, dbCfg.MaintenanceDB, "postgres")

	switch dbType {
	case "sqlite", "sqlite3":
		if dsn == "" {
			root := firstNonEmpty(cfg.Root, dbCfg.Root)
			if root == "" {
				root = filepath.Join(".adk", "data", "database")
			}
			dsn = filepath.Join(root, "adk.db")
		}
		if !looksLikeMemorySQLiteDSN(dsn) {
			if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
				return nil, fmt.Errorf("create sqlite session database directory: %w", err)
			}
		}
		ss, err := sessiondatabase.NewSessionService(sqlite.Open(dsn))
		if err != nil {
			return nil, err
		}
		if autoMigrate {
			if err := sessiondatabase.AutoMigrate(ss); err != nil {
				return nil, err
			}
		}
		return ss, nil
	case "postgres", "postgresql", "pg":
		if dsn == "" {
			return nil, fmt.Errorf("postgres session service requires storage.session.dsn/storage.session.dsn_env or storage.database.dsn/storage.database.dsn_env")
		}
		if autoCreateDatabase {
			if err := pgutil.EnsureDatabase(context.Background(), dsn, pgutil.EnsureOptions{MaintenanceDatabase: maintenanceDB}); err != nil {
				return nil, fmt.Errorf("ensure postgres session database: %w", err)
			}
		}
		ss, err := session.PostgresService(context.Background(), session.PostgresConfig{DSN: dsn, AutoMigrate: autoMigrate, MaxConns: int32(firstPositive(cfg.MaxOpenConns, dbCfg.MaxOpenConns))})
		if err != nil {
			return nil, err
		}
		return ss, nil
	case "mysql":
		if dsn == "" {
			return nil, fmt.Errorf("mysql session service requires storage.session.dsn/storage.session.dsn_env or storage.database.dsn/storage.database.dsn_env")
		}
		return nil, fmt.Errorf("mysql session service is configured but the mysql GORM driver is not linked in this build yet; use postgres or sqlite")
	default:
		return nil, fmt.Errorf("unsupported database session type %q", dbType)
	}
}

func looksLikeMemorySQLiteDSN(dsn string) bool {
	dsn = strings.TrimSpace(strings.ToLower(dsn))
	return dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:")
}

func buildArtifactService(cfg ServiceConfig) (artifact.Service, error) {
	switch normalizeType(cfg.Type) {
	case "", "filesystem", "file", "fs", "localfs":
		return artifact.FileSystemService(cfg.Root)
	case "memory", "inmemory", "in_memory":
		return artifact.InMemoryService(), nil
	case "minio", "s3":
		accessKey := cfg.AccessKey
		if accessKey == "" && cfg.AccessKeyEnv != "" {
			accessKey = os.Getenv(cfg.AccessKeyEnv)
		}
		secretKey := cfg.SecretKey
		if secretKey == "" && cfg.SecretKeyEnv != "" {
			secretKey = os.Getenv(cfg.SecretKeyEnv)
		}
		return minioartifact.NewService(context.Background(), minioartifact.Config{
			Endpoint:        cfg.Endpoint,
			Bucket:          cfg.Bucket,
			AccessKey:       accessKey,
			SecretKey:       secretKey,
			Region:          cfg.Region,
			UseSSL:          cfg.UseSSL,
			Prefix:          cfg.Prefix,
			CreateBucket:    cfg.CreateBucket,
			LookupPathStyle: cfg.PathStyle,
		})
	default:
		return nil, fmt.Errorf("unsupported artifact service type %q", cfg.Type)
	}
}

func buildMemoryService(cfg ServiceConfig) (memory.Service, error) {
	switch normalizeType(cfg.Type) {
	case "", "filesystem", "file", "fs", "localfs":
		return memory.FileSystemService(cfg.Root)
	case "memory", "inmemory", "in_memory":
		return memory.InMemoryService(), nil
	default:
		return nil, fmt.Errorf("unsupported memory service type %q", cfg.Type)
	}
}

func normalizeType(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (c *Config) ResolveModelSpec(ref string) (string, ModelSpec, bool) {
	if ref == "" {
		ref = c.Models.Default
	}
	if alias, ok := c.Models.Aliases[ref]; ok {
		ref = alias
	}
	if spec, ok := c.Models.Specs[ref]; ok {
		return ref, spec, true
	}
	return ref, ModelSpec{}, false
}

// NewModel resolves an agent `model:` reference into a concrete LLM provider.
func NewModel(ctx context.Context, ref string) (adkmodel.LLM, string, error) {
	cfg := FromContext(ctx)
	id, spec, ok := cfg.ResolveModelSpec(ref)
	if !ok {
		// `default` and alias-like names are configuration references, not concrete
		// provider model ids. Do not silently pass them to OpenAI-compatible
		// backends, otherwise the real error becomes a late HTTP 404 such as:
		// "The model `default` does not exist".
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" || trimmed == "default" || !looksLikeConcreteModelName(trimmed) {
			return nil, id, fmt.Errorf("model reference %q is not defined in adk runtime config; configure models.aliases.%s or models.specs.%s", ref, safeConfigKey(trimmed), safeConfigKey(trimmed))
		}
		return newLegacyModel(ctx, ref)
	}
	provider := normalizeType(spec.Provider)
	if provider == "" {
		if googlellm.IsGeminiModel(spec.Model) {
			provider = "gemini"
		} else {
			provider = "openai"
		}
	}
	modelName := firstNonEmpty(spec.Model, ref)
	modelName = strings.TrimPrefix(modelName, "openai/")
	modelName = strings.TrimPrefix(modelName, "gemini/")

	switch provider {
	case "openai", "openai_compatible", "openai-compatible":
		opts := []openai.Option{}
		if spec.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(spec.BaseURL))
		}
		apiKey := spec.APIKey
		if apiKey == "" && spec.APIKeyEnv != "" {
			apiKey = os.Getenv(spec.APIKeyEnv)
		}
		if apiKey != "" {
			opts = append(opts, openai.WithAPIKey(apiKey))
		}
		if len(spec.Headers) > 0 {
			for k, v := range spec.Headers {
				opts = append(opts, openai.WithHeader(k, v))
			}
		}
		if spec.StrictTools {
			opts = append(opts, openai.WithStrictTools(true))
		}
		extra := mergeMaps(spec.ExtraBody, spec.Generation.ExtraBody)
		if spec.Generation.MaxOutputTokens != nil {
			if _, ok := extra["max_tokens"]; !ok {
				extra["max_tokens"] = *spec.Generation.MaxOutputTokens
			}
		}
		if spec.Generation.TopP != nil {
			if _, ok := extra["top_p"]; !ok {
				extra["top_p"] = *spec.Generation.TopP
			}
		}
		if len(extra) > 0 {
			opts = append(opts, openai.WithExtraBody(extra))
		}
		if spec.Timeout != "" {
			if d, err := time.ParseDuration(spec.Timeout); err == nil && d > 0 {
				opts = append(opts, openai.WithHTTPClient(&http.Client{Timeout: d}))
			}
		}
		llm, err := openai.NewModel(modelName, opts...)
		return llm, id, err
	case "gemini", "google", "vertexai":
		llm, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{APIKey: firstNonEmpty(spec.APIKey, envValue(spec.APIKeyEnv), os.Getenv("GOOGLE_API_KEY"))})
		return llm, id, err
	default:
		return nil, id, fmt.Errorf("unsupported model provider %q for model spec %q", spec.Provider, id)
	}
}

func looksLikeConcreteModelName(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	if strings.HasPrefix(ref, "openai/") || strings.HasPrefix(ref, "gemini/") {
		return true
	}
	// Most concrete model names include a provider/model family separator or a
	// version marker, for example gpt-4.1-mini, deepseek-chat, qwen-plus.
	// Short aliases such as default/deepseek/fast are intentionally rejected here
	// unless they are present in models.aliases or models.specs.
	return strings.ContainsAny(ref, "-/.")
}

func safeConfigKey(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "default"
	}
	return ref
}

func newLegacyModel(ctx context.Context, ref string) (adkmodel.LLM, string, error) {
	if ref == "" {
		ref = "openai/default"
	}
	if googlellm.IsGeminiModel(ref) {
		llm, err := gemini.NewModel(ctx, ref, &genai.ClientConfig{APIKey: os.Getenv("GOOGLE_API_KEY")})
		return llm, ref, err
	}
	modelName := strings.TrimPrefix(ref, "openai/")
	llm, err := openai.NewModel(modelName)
	return llm, ref, err
}

func (c *Config) DefaultGenerateContentConfigFor(ref string) *genai.GenerateContentConfig {
	_, spec, ok := c.ResolveModelSpec(ref)
	if !ok {
		return nil
	}
	return spec.Generation.toGenAI()
}

func (g GenerationConfig) toGenAI() *genai.GenerateContentConfig {
	out := &genai.GenerateContentConfig{}
	setPtrField(out, "Temperature", g.Temperature)
	setPtrField(out, "TopP", g.TopP)
	setPtrField(out, "TopK", g.TopK)
	setValueField(out, "MaxOutputTokens", g.MaxOutputTokens)
	setValueField(out, "CandidateCount", g.CandidateCount)
	setValueField(out, "StopSequences", g.StopSequences)
	setValueField(out, "ResponseMIMEType", g.ResponseMIMEType)
	if isZeroStruct(out) {
		return nil
	}
	return out
}

func MergeGenerateContentConfig(base, override *genai.GenerateContentConfig) *genai.GenerateContentConfig {
	if base == nil && override == nil {
		return nil
	}
	out := &genai.GenerateContentConfig{}
	mergeStruct(out, base)
	mergeStruct(out, override)
	return out
}

func (c *Config) DefaultLauncherArgs() []string {
	if len(c.Server.DefaultArgs) > 0 {
		return append([]string(nil), c.Server.DefaultArgs...)
	}
	if c.Server.Console.Enabled {
		return []string{"console"}
	}
	if !c.Server.Web.Enabled {
		return nil
	}
	args := []string{"web"}
	if c.Server.Web.Port > 0 {
		args = append(args, "-port", strconv.Itoa(c.Server.Web.Port))
	}
	if c.Server.Web.WriteTimeout != "" {
		args = append(args, "-write-timeout", c.Server.Web.WriteTimeout)
	}
	if c.Server.Web.ReadTimeout != "" {
		args = append(args, "-read-timeout", c.Server.Web.ReadTimeout)
	}
	if c.Server.Web.IdleTimeout != "" {
		args = append(args, "-idle-timeout", c.Server.Web.IdleTimeout)
	}
	if c.Server.Web.ShutdownTimeout != "" {
		args = append(args, "-shutdown-timeout", c.Server.Web.ShutdownTimeout)
	}
	if c.Server.Web.OtelToCloud {
		args = append(args, "-otel_to_cloud=true")
	}
	if c.Server.WebUI.Enabled {
		args = append(args, "webui")
		if c.Server.WebUI.APIServerAddress != "" {
			args = append(args, "-api_server_address", c.Server.WebUI.APIServerAddress)
		}
	}
	if c.Server.API.Enabled {
		args = append(args, "api")
		if c.Server.API.FrontendAddress != "" {
			args = append(args, "-webui_address", c.Server.API.FrontendAddress)
		}
		if c.Server.API.PathPrefix != "" {
			args = append(args, "-path_prefix", c.Server.API.PathPrefix)
		}
		if c.Server.API.SSEWriteTimeout != "" {
			args = append(args, "-sse-write-timeout", c.Server.API.SSEWriteTimeout)
		}
		if c.Server.API.TraceCapacity > 0 {
			args = append(args, "-trace_capacity", strconv.Itoa(c.Server.API.TraceCapacity))
		}
	}
	return args
}

func Describe(cfg *Config) string {
	if cfg == nil {
		return "runtime config: <nil>"
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b)
}

func absPath(cwd, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func envValue(key string) string {
	if key == "" {
		return ""
	}
	return os.Getenv(key)
}

func int32Ptr(v int32) *int32 { return &v }

func mergeMaps(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func setPtrField(target any, name string, value any) {
	if value == nil {
		return
	}
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	field := v.Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		return
	}
	val := reflect.ValueOf(value)
	if val.Type().AssignableTo(field.Type()) {
		field.Set(val)
	}
}

func setValueField(target any, name string, value any) {
	if value == nil {
		return
	}
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	field := v.Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		return
	}
	val := reflect.ValueOf(value)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return
		}
		val = val.Elem()
	}
	if val.Type().AssignableTo(field.Type()) {
		field.Set(val)
		return
	}
	if val.Type().ConvertibleTo(field.Type()) {
		field.Set(val.Convert(field.Type()))
	}
}

func isZeroStruct(v any) bool {
	return reflect.DeepEqual(v, &genai.GenerateContentConfig{})
}

func mergeStruct(dst, src any) {
	if dst == nil || src == nil {
		return
	}
	dv := reflect.ValueOf(dst)
	sv := reflect.ValueOf(src)
	if dv.Kind() != reflect.Pointer || sv.Kind() != reflect.Pointer || dv.IsNil() || sv.IsNil() {
		return
	}
	dv = dv.Elem()
	sv = sv.Elem()
	if dv.Kind() != reflect.Struct || sv.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < sv.NumField(); i++ {
		field := sv.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		sfv := sv.Field(i)
		if sfv.IsZero() {
			continue
		}
		dfv := dv.FieldByName(field.Name)
		if dfv.IsValid() && dfv.CanSet() && sfv.Type().AssignableTo(dfv.Type()) {
			dfv.Set(sfv)
		}
	}
}
