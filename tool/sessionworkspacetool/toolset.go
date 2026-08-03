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

// Package sessionworkspacetool provides a small session-scoped filesystem
// workspace for temporary agent outputs.
//
// Design goals:
//   - One session owns one isolated workspace.
//   - Large intermediate files stay on disk and are referenced by path/ref.
//   - Tool results returned to the model are compact metadata, not file bodies.
//   - Worker agents may use their own session workspace and commit selected
//     outputs back to a parent session workspace.
//   - Publishing is explicit; discard is explicit.
package sessionworkspacetool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const defaultMaxInlineReadChars = 12000

// Config configures SessionWorkspaceToolset.
type Config struct {
	Root               string
	EnabledTools       map[string]bool
	MaxInlineReadChars int
	ForbidBatchNo      bool
}

// ConfigFromMap decodes YAML args.
func ConfigFromMap(args map[string]any) Config {
	root := strings.TrimSpace(os.Getenv("ADK_SESSION_WORKSPACE_ROOT"))
	if root == "" {
		root = filepath.Join(".adk", "data", "workspaces")
	}
	cfg := Config{
		Root:               root,
		EnabledTools:       map[string]bool{},
		MaxInlineReadChars: defaultMaxInlineReadChars,
	}
	if args == nil {
		return cfg
	}
	if v, ok := args["root"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.Root = os.ExpandEnv(strings.TrimSpace(v))
	}
	if v, ok := args["max_inline_read_chars"]; ok {
		cfg.MaxInlineReadChars = intFromAny(v, defaultMaxInlineReadChars)
	}
	if v, ok := args["forbid_batch_no"]; ok {
		cfg.ForbidBatchNo = boolFromAny(v)
	}
	for _, name := range anySliceToStrings(args["enabled_tools"]) {
		if name != "" {
			cfg.EnabledTools[name] = true
		}
	}
	return cfg
}

// NewToolset creates a session workspace toolset.
func NewToolset(cfg Config) (tool.Toolset, error) {
	if cfg.MaxInlineReadChars <= 0 {
		cfg.MaxInlineReadChars = defaultMaxInlineReadChars
	}
	ts := &Toolset{cfg: cfg}
	builders := map[string]func() (tool.Tool, error){
		"session_workspace_info": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_info",
				Description: "Return compact metadata about the current session workspace. This does not read file contents.",
			}, ts.Info)
		},
		"session_workspace_init_run": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_init_run",
				Description: "Initialize a temporary run folder inside the current session workspace. Use this before batch work. Returns only refs and metadata.",
			}, ts.InitRun)
		},
		"session_workspace_write_file": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_write_file",
				Description: "Write a file into the current session workspace. Returns a workspace ref, not the file content. Use this for batch_summary.json, batch_analysis.md, skill_delta.json, current_skill.md.",
			}, ts.WriteFile)
		},
		"session_workspace_read_file": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_read_file",
				Description: "Read a small file from the current session workspace with strict max_chars truncation. Do not use this for raw chapters or huge outputs.",
			}, ts.ReadFile)
		},
		"session_workspace_commit_to_parent": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_commit_to_parent",
				Description: "Commit selected output files from the current worker session workspace to a parent session workspace. This implements fork_commit for sub-agent workers. It never commits raw_chapters.json or chapter_text/* unless explicitly included and allowed by policy.",
			}, ts.CommitToParent)
		},
		"session_workspace_publish_run": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_publish_run",
				Description: "Publish a run from the current session workspace to a durable published workspace folder. Use only after user approval.",
			}, ts.PublishRun)
		},
		"session_workspace_discard_run": func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "session_workspace_discard_run",
				Description: "Discard a run folder from the current session workspace. Requires confirm=true.",
			}, ts.DiscardRun)
		},
	}
	keys := make([]string, 0, len(builders))
	for name := range builders {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if len(cfg.EnabledTools) > 0 && !cfg.EnabledTools[name] {
			continue
		}
		built, err := builders[name]()
		if err != nil {
			return nil, err
		}
		ts.tools = append(ts.tools, built)
	}
	return ts, nil
}

// Toolset groups workspace tools.
type Toolset struct {
	cfg   Config
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "SessionWorkspaceToolset" }
func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return t.tools, nil
}

type InfoArgs struct {
	RunID string `json:"run_id,omitempty" jsonschema:"Optional run id. If present, run_path/ref is also returned."`
}

type InitRunArgs struct {
	RunID      string         `json:"run_id" jsonschema:"Stable run id, for example dialogue_writing__tangqi__run_001."`
	BookID     string         `json:"book_id,omitempty"`
	BookName   string         `json:"book_name,omitempty"`
	SkillFocus string         `json:"skill_focus,omitempty" jsonschema:"For this workflow use dialogue_writing."`
	Objective  string         `json:"objective,omitempty"`
	BatchSize  int            `json:"batch_size,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type WriteFileArgs struct {
	RunID         string `json:"run_id"`
	BatchNo       int    `json:"batch_no,omitempty" jsonschema:"If set, file is written under batch_XXXX/."`
	Path          string `json:"path,omitempty" jsonschema:"Relative path under the run folder. If empty, file_name is used."`
	FileName      string `json:"file_name,omitempty" jsonschema:"File name when path is not provided."`
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
}

type ReadFileArgs struct {
	RunID    string `json:"run_id"`
	Path     string `json:"path" jsonschema:"Relative path under the run folder."`
	MaxChars int    `json:"max_chars,omitempty"`
}

type CommitToParentArgs struct {
	ParentSessionID string   `json:"parent_session_id" jsonschema:"Parent/manager ADK session id."`
	RunID           string   `json:"run_id"`
	BatchNo         int      `json:"batch_no,omitempty"`
	Include         []string `json:"include,omitempty" jsonschema:"Relative paths under the run folder or batch folder to commit. Defaults to safe batch outputs."`
	Overwrite       bool     `json:"overwrite,omitempty"`
}

type PublishRunArgs struct {
	RunID     string `json:"run_id"`
	ProjectID string `json:"project_id" jsonschema:"Durable project id to publish into."`
	Name      string `json:"name,omitempty"`
	Confirm   bool   `json:"confirm" jsonschema:"Must be true; publishing is explicit."`
}

type DiscardRunArgs struct {
	RunID   string `json:"run_id"`
	Confirm bool   `json:"confirm" jsonschema:"Must be true to discard workspace data."`
}

func (t *Toolset) Info(ctx tool.Context, args InfoArgs) (map[string]any, error) {
	root, err := t.rootAbs()
	if err != nil {
		return nil, err
	}
	sw := t.sessionWorkspace(ctx)
	out := map[string]any{
		"app_name":          ctx.AppName(),
		"user_id":           ctx.UserID(),
		"session_id":        ctx.SessionID(),
		"workspace_root":    root,
		"session_workspace": sw,
		"session_ref":       fmt.Sprintf("workspace:session/%s", ctx.SessionID()),
		"policy": map[string]any{
			"large_contents": "store_in_workspace_return_refs_only",
			"publish":        "explicit",
			"discard":        "explicit",
		},
	}
	if args.RunID != "" {
		rp, err := t.runPath(ctx.SessionID(), args.RunID)
		if err != nil {
			return nil, err
		}
		out["run_path"] = rp
		out["run_ref"] = t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID))
	}
	return out, nil
}

func (t *Toolset) InitRun(ctx tool.Context, args InitRunArgs) (map[string]any, error) {
	if strings.TrimSpace(args.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	runDir, err := t.runPath(ctx.SessionID(), args.RunID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, err
	}
	state := map[string]any{
		"run_id":        args.RunID,
		"book_id":       args.BookID,
		"book_name":     args.BookName,
		"skill_focus":   args.SkillFocus,
		"objective":     args.Objective,
		"batch_size":    args.BatchSize,
		"metadata":      args.Metadata,
		"status":        "running",
		"app_name":      ctx.AppName(),
		"user_id":       ctx.UserID(),
		"session_id":    ctx.SessionID(),
		"created_at":    time.Now().Format(time.RFC3339),
		"workspace_ref": t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID)),
	}
	if err := writeJSON(filepath.Join(runDir, "run_state.json"), state); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":        "created",
		"run_id":        args.RunID,
		"run_ref":       t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID)),
		"run_state_ref": t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID, "run_state.json")),
		"session_id":    ctx.SessionID(),
	}, nil
}

func (t *Toolset) WriteFile(ctx tool.Context, args WriteFileArgs) (map[string]any, error) {
	if strings.TrimSpace(args.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	if t.cfg.ForbidBatchNo && args.BatchNo > 0 {
		return nil, fmt.Errorf("batch_no is disabled for this workspace toolset; use explicit standard path")
	}
	if args.Content != "" && args.ContentBase64 != "" {
		return nil, fmt.Errorf("use either content or content_base64, not both")
	}
	var data []byte
	var err error
	if args.ContentBase64 != "" {
		data, err = base64.StdEncoding.DecodeString(args.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("invalid content_base64: %w", err)
		}
	} else {
		data = []byte(args.Content)
	}
	rel, err := writeRelPath(args.BatchNo, args.Path, args.FileName)
	if err != nil {
		return nil, err
	}
	runDir, err := t.runPath(ctx.SessionID(), args.RunID)
	if err != nil {
		return nil, err
	}
	abs, err := safeJoin(runDir, rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":     "written",
		"run_id":     args.RunID,
		"path":       rel,
		"ref":        t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID, rel)),
		"bytes":      len(data),
		"mime_type":  args.MimeType,
		"session_id": ctx.SessionID(),
	}, nil
}

func (t *Toolset) ReadFile(ctx tool.Context, args ReadFileArgs) (map[string]any, error) {
	if strings.TrimSpace(args.RunID) == "" || strings.TrimSpace(args.Path) == "" {
		return nil, fmt.Errorf("run_id and path are required")
	}
	runDir, err := t.runPath(ctx.SessionID(), args.RunID)
	if err != nil {
		return nil, err
	}
	abs, err := safeJoin(runDir, args.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	maxChars := args.MaxChars
	if maxChars <= 0 || maxChars > t.cfg.MaxInlineReadChars {
		maxChars = t.cfg.MaxInlineReadChars
	}
	content := string(data)
	truncated := false
	if len([]rune(content)) > maxChars {
		runes := []rune(content)
		content = string(runes[:maxChars])
		truncated = true
	}
	return map[string]any{
		"run_id":     args.RunID,
		"path":       filepath.ToSlash(filepath.Clean(args.Path)),
		"ref":        t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID, args.Path)),
		"bytes":      len(data),
		"max_chars":  maxChars,
		"truncated":  truncated,
		"content":    content,
		"session_id": ctx.SessionID(),
	}, nil
}

func (t *Toolset) CommitToParent(ctx tool.Context, args CommitToParentArgs) (map[string]any, error) {
	if strings.TrimSpace(args.ParentSessionID) == "" {
		return nil, fmt.Errorf("parent_session_id is required")
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	if t.cfg.ForbidBatchNo && args.BatchNo > 0 {
		return nil, fmt.Errorf("batch_no is disabled for this workspace toolset; include explicit standard paths")
	}
	sourceRun, err := t.runPath(ctx.SessionID(), args.RunID)
	if err != nil {
		return nil, err
	}
	destRun, err := t.runPath(args.ParentSessionID, args.RunID)
	if err != nil {
		return nil, err
	}
	include := args.Include
	if len(include) == 0 {
		if args.BatchNo > 0 {
			prefix := fmt.Sprintf("batch_%04d", args.BatchNo)
			include = []string{
				filepath.Join(prefix, "batch_summary.json"),
				filepath.Join(prefix, "batch_analysis.md"),
				filepath.Join(prefix, "skill_delta.json"),
				filepath.Join(prefix, "current_skill.md"),
			}
		} else {
			include = []string{"run_state.json", "current/current_skill.md", "current/index.json"}
		}
	}
	committed := []map[string]any{}
	for _, rel := range include {
		clean, err := cleanRel(rel)
		if err != nil {
			return nil, err
		}
		if isForbiddenCommit(clean) {
			return nil, fmt.Errorf("refusing to commit forbidden raw content path %q", rel)
		}
		src, err := safeJoin(sourceRun, clean)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst, err := safeJoin(destRun, clean)
		if err != nil {
			return nil, err
		}
		if !args.Overwrite {
			if _, err := os.Stat(dst); err == nil {
				return nil, fmt.Errorf("destination exists: %s; pass overwrite=true", clean)
			}
		}
		if err := copyFile(src, dst); err != nil {
			return nil, err
		}
		info, _ := os.Stat(dst)
		var size int64
		if info != nil {
			size = info.Size()
		}
		committed = append(committed, map[string]any{
			"path":  clean,
			"ref":   t.ref(args.ParentSessionID, filepath.Join("runs", args.RunID, clean)),
			"bytes": size,
		})
	}
	return map[string]any{
		"status":            "committed",
		"run_id":            args.RunID,
		"source_session_id": ctx.SessionID(),
		"parent_session_id": args.ParentSessionID,
		"committed":         committed,
		"count":             len(committed),
	}, nil
}

func (t *Toolset) PublishRun(ctx tool.Context, args PublishRunArgs) (map[string]any, error) {
	if !args.Confirm {
		return nil, fmt.Errorf("confirm=true is required to publish")
	}
	if strings.TrimSpace(args.RunID) == "" || strings.TrimSpace(args.ProjectID) == "" {
		return nil, fmt.Errorf("run_id and project_id are required")
	}
	source, err := t.runPath(ctx.SessionID(), args.RunID)
	if err != nil {
		return nil, err
	}
	root, err := t.rootAbs()
	if err != nil {
		return nil, err
	}
	project, err := cleanRel(args.ProjectID)
	if err != nil {
		return nil, err
	}
	dest, err := safeJoin(filepath.Join(root, "published", project), args.RunID)
	if err != nil {
		return nil, err
	}
	if err := copyDir(source, dest); err != nil {
		return nil, err
	}
	meta := map[string]any{
		"run_id":       args.RunID,
		"project_id":   args.ProjectID,
		"name":         args.Name,
		"source_ref":   t.ref(ctx.SessionID(), filepath.Join("runs", args.RunID)),
		"published":    time.Now().Format(time.RFC3339),
		"published_by": map[string]string{"app_name": ctx.AppName(), "user_id": ctx.UserID(), "session_id": ctx.SessionID()},
	}
	_ = writeJSON(filepath.Join(dest, "published_meta.json"), meta)
	return map[string]any{
		"status":        "published",
		"project_id":    args.ProjectID,
		"run_id":        args.RunID,
		"published_ref": fmt.Sprintf("workspace:published/%s/%s", args.ProjectID, args.RunID),
	}, nil
}

func (t *Toolset) DiscardRun(ctx tool.Context, args DiscardRunArgs) (map[string]any, error) {
	if !args.Confirm {
		return nil, fmt.Errorf("confirm=true is required to discard")
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	runDir, err := t.runPath(ctx.SessionID(), args.RunID)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(runDir); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":     "discarded",
		"run_id":     args.RunID,
		"session_id": ctx.SessionID(),
	}, nil
}

func (t *Toolset) rootAbs() (string, error) {
	root := os.ExpandEnv(t.cfg.Root)
	if root == "" {
		root = filepath.Join(".adk", "data", "workspaces")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func (t *Toolset) sessionWorkspace(ctx tool.Context) string {
	root, _ := t.rootAbs()
	return filepath.Join(root, "sessions", safeName(ctx.SessionID()))
}

func (t *Toolset) sessionPath(sessionID string) (string, error) {
	root, err := t.rootAbs()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session_id is required")
	}
	return filepath.Join(root, "sessions", safeName(sessionID)), nil
}

func (t *Toolset) runPath(sessionID, runID string) (string, error) {
	sp, err := t.sessionPath(sessionID)
	if err != nil {
		return "", err
	}
	cleanRun, err := cleanRel(runID)
	if err != nil {
		return "", fmt.Errorf("invalid run_id: %w", err)
	}
	return safeJoin(sp, filepath.Join("runs", cleanRun))
}

func (t *Toolset) ref(sessionID, rel string) string {
	return "workspace:session/" + safeName(sessionID) + "/" + filepath.ToSlash(filepath.Clean(rel))
}

func writeRelPath(batchNo int, path, fileName string) (string, error) {
	base := strings.TrimSpace(path)
	if base == "" {
		base = strings.TrimSpace(fileName)
	}
	if base == "" {
		return "", fmt.Errorf("path or file_name is required")
	}
	if batchNo > 0 && !strings.HasPrefix(filepath.ToSlash(base), fmt.Sprintf("batch_%04d/", batchNo)) {
		base = filepath.Join(fmt.Sprintf("batch_%04d", batchNo), base)
	}
	return cleanRel(base)
}

func cleanRel(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" || p == "." {
		return "", fmt.Errorf("path cannot be empty")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", p)
	}
	clean := filepath.Clean(p)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal is not allowed: %q", p)
	}
	return clean, nil
}

func safeJoin(root, rel string) (string, error) {
	clean, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(absRoot, clean)
	if !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) && abs != absRoot {
		return "", fmt.Errorf("resolved path escaped workspace")
	}
	return abs, nil
}

func isForbiddenCommit(rel string) bool {
	slash := filepath.ToSlash(rel)
	return slash == "raw_chapters.json" || strings.HasPrefix(slash, "chapter_text/") || strings.Contains(slash, "/chapter_text/")
}

func safeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if isForbiddenCommit(rel) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func anySliceToStrings(v any) []string {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func intFromAny(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case float32:
		return int(x)
	case json.Number:
		i, err := x.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		var i int
		if _, err := fmt.Sscanf(x, "%d", &i); err == nil {
			return i
		}
	}
	return def
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "t", "true", "y", "yes", "on":
			return true
		}
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case float32:
		return x != 0
	case json.Number:
		i, err := x.Int64()
		return err == nil && i != 0
	}
	return false
}
