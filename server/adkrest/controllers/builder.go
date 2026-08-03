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

package controllers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

// BuilderConfig configures the minimal filesystem-backed builder endpoints
// used by the embedded ADK WebUI.
type BuilderConfig struct {
	AppsRoot     string
	TmpRoot      string
	DefaultModel string
}

// BuilderAPIController handles WebUI builder endpoints.
type BuilderAPIController struct {
	cfg BuilderConfig
}

// NewBuilderAPIController creates a controller for WebUI builder APIs.
func NewBuilderAPIController(cfg BuilderConfig) *BuilderAPIController {
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "default"
	}
	return &BuilderAPIController{cfg: cfg}
}

// SaveHandler handles POST /builder/save and POST /builder/save?tmp=true.
// The WebUI sends multipart/form-data with a repeated field named "files".
// Each uploaded filename is a relative path like "my_app/root_agent.yaml".
func (c *BuilderAPIController) SaveHandler(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusOK)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetRoot := c.targetRoot(req)
	if targetRoot == "" {
		http.Error(rw, "builder root is not configured", http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		http.Error(rw, fmt.Sprintf("create builder root: %v", err), http.StatusInternalServerError)
		return
	}

	if err := req.ParseMultipartForm(64 << 20); err != nil {
		http.Error(rw, fmt.Sprintf("parse multipart form: %v", err), http.StatusBadRequest)
		return
	}
	files := req.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(rw, "no uploaded files found in multipart field 'files'", http.StatusBadRequest)
		return
	}

	type upload struct {
		rel  string
		data []byte
	}
	uploads := make([]upload, 0, len(files))
	appName := ""
	for _, header := range files {
		rel, err := cleanBuilderRelPath(header.Filename)
		if err != nil {
			http.Error(rw, fmt.Sprintf("invalid upload path %q: %v", header.Filename, err), http.StatusBadRequest)
			return
		}
		src, err := header.Open()
		if err != nil {
			http.Error(rw, fmt.Sprintf("open uploaded file: %v", err), http.StatusInternalServerError)
			return
		}
		data, readErr := io.ReadAll(src)
		closeErr := src.Close()
		if readErr != nil {
			http.Error(rw, fmt.Sprintf("read uploaded file: %v", readErr), http.StatusInternalServerError)
			return
		}
		if closeErr != nil {
			http.Error(rw, fmt.Sprintf("close uploaded file: %v", closeErr), http.StatusInternalServerError)
			return
		}

		if strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml") {
			data = rewriteBuilderDefaultModel(data, c.cfg.DefaultModel)
		}
		if appName == "" && filepath.Base(rel) == "root_agent.yaml" {
			if name := extractBuilderAppName(data); name != "" {
				appName = name
			}
		}
		uploads = append(uploads, upload{rel: rel, data: data})
	}

	// The embedded WebUI creates File objects whose names include paths like
	// "my_app/root_agent.yaml". Some browsers strip path separators from the
	// multipart filename and the backend receives just "root_agent.yaml". When
	// that happens, infer the app directory from the root agent yaml name and
	// restore the expected apps_root/<app>/file layout. Without this, a newly
	// created app is written to .adk/builder/tmp/root_agent.yaml and list-apps
	// cannot discover it as .adk/builder/tmp/<app>/root_agent.yaml.
	if appName != "" {
		for i := range uploads {
			if filepath.Dir(uploads[i].rel) == "." {
				rel, err := cleanBuilderRelPath(filepath.Join(appName, uploads[i].rel))
				if err != nil {
					http.Error(rw, fmt.Sprintf("invalid inferred upload path: %v", err), http.StatusBadRequest)
					return
				}
				uploads[i].rel = rel
			}
		}
	}

	for _, upload := range uploads {
		dst := filepath.Join(targetRoot, upload.rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			http.Error(rw, fmt.Sprintf("create app directory: %v", err), http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(dst, upload.data, 0o644); err != nil {
			http.Error(rw, fmt.Sprintf("write %s: %v", upload.rel, err), http.StatusInternalServerError)
			return
		}
	}

	EncodeJSONResponse(true, http.StatusOK, rw)
}

// GetAppHandler handles GET /builder/app/{app} and optional file_path/tmp.
func (c *BuilderAPIController) GetAppHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	app, err := cleanPathSegment(vars["app"])
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid app name: %v", err), http.StatusBadRequest)
		return
	}
	filePath := req.URL.Query().Get("file_path")
	if filePath == "" {
		filePath = "root_agent.yaml"
	}
	rel, err := cleanBuilderRelPath(filepath.Join(app, filePath))
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid file path: %v", err), http.StatusBadRequest)
		return
	}

	roots := []string{c.targetRoot(req)}
	// When editing tmp, fall back to the saved app so manually created backend
	// agents still open in the builder even before a tmp copy exists.
	if req.URL.Query().Get("tmp") == "true" {
		roots = append(roots, c.cfg.AppsRoot)
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if err == nil {
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write(data)
			return
		}
	}
	for _, root := range roots {
		data, ok, err := c.readNestedAppFileByBase(root, app, filePath)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusConflict)
			return
		}
		if ok {
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write(data)
			return
		}
	}
	http.NotFound(rw, req)
}

// SaveAppFileHandler handles PUT /builder/app/{app}/file/{file_path}.
// It is intentionally small and filesystem-backed so the Admin Agent Studio can
// edit the actual YAML files that the runtime scans under the builder apps root.
func (c *BuilderAPIController) SaveAppFileHandler(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusOK)
		return
	}
	if req.Method != http.MethodPut && req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vars := mux.Vars(req)
	app, err := cleanPathSegment(vars["app"])
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid app name: %v", err), http.StatusBadRequest)
		return
	}
	filePath := vars["file_path"]
	if filePath == "" {
		filePath = req.URL.Query().Get("file_path")
	}
	if filePath == "" {
		filePath = "root_agent.yaml"
	}
	filePath = strings.TrimPrefix(strings.ReplaceAll(filePath, "\\", "/"), app+"/")
	rel, err := cleanBuilderRelPath(filepath.Join(app, filePath))
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid file path: %v", err), http.StatusBadRequest)
		return
	}

	targetRoot := c.targetRoot(req)
	if targetRoot == "" {
		http.Error(rw, "builder root is not configured", http.StatusInternalServerError)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(rw, req.Body, 4<<20))
	if err != nil {
		http.Error(rw, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
		return
	}
	if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		data = rewriteBuilderDefaultModel(data, c.cfg.DefaultModel)
	}

	dst := filepath.Join(targetRoot, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		http.Error(rw, fmt.Sprintf("create app directory: %v", err), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		http.Error(rw, fmt.Sprintf("write %s: %v", rel, err), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true, "app": app, "path": filepath.ToSlash(filePath)}, http.StatusOK, rw)
}

// CancelHandler handles POST /builder/app/{app}/cancel by deleting tmp files.
func (c *BuilderAPIController) CancelHandler(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusOK)
		return
	}
	vars := mux.Vars(req)
	app, err := cleanPathSegment(vars["app"])
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid app name: %v", err), http.StatusBadRequest)
		return
	}
	if c.cfg.TmpRoot != "" {
		_ = os.RemoveAll(filepath.Join(c.cfg.TmpRoot, app))
	}
	EncodeJSONResponse(true, http.StatusOK, rw)
}

// BuildGraphHandler handles GET /dev/build_graph/{app}. The current minimal
// implementation returns a graph from yaml filenames; it avoids 404s in the UI
// and can be replaced later by a semantic agent graph builder.
func (c *BuilderAPIController) BuildGraphHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	app, err := cleanPathSegment(vars["app"])
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid app name: %v", err), http.StatusBadRequest)
		return
	}
	nodes, edges := c.buildGraph(app)
	EncodeJSONResponse(map[string]any{"nodes": nodes, "edges": edges}, http.StatusOK, rw)
}

// BuildGraphImageHandler handles GET /dev/build_graph_image/{app}. The WebUI
// renders the returned DOT client-side, so this endpoint returns dotSrc rather
// than a binary image.
func (c *BuilderAPIController) BuildGraphImageHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	app, err := cleanPathSegment(vars["app"])
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid app name: %v", err), http.StatusBadRequest)
		return
	}
	nodes, edges := c.buildGraph(app)
	EncodeJSONResponse(map[string]any{
		app: map[string]any{
			"dotSrc": buildDOT(app, nodes, edges),
		},
	}, http.StatusOK, rw)
}

func (c *BuilderAPIController) buildGraph(app string) ([]map[string]any, []map[string]any) {
	root := filepath.Join(firstNonEmpty(c.cfg.TmpRoot, c.cfg.AppsRoot), app)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		root = filepath.Join(c.cfg.AppsRoot, app)
	}
	rootConfig := filepath.Join(root, "root_agent.yaml")
	if _, err := os.Stat(rootConfig); err == nil {
		return c.buildSemanticGraph(app, rootConfig)
	}

	nodes := []map[string]any{}
	edges := []map[string]any{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		if name == "root_agent.yaml" {
			id = app
		}
		nodes = append(nodes, map[string]any{"id": id, "label": id, "file": name})
		return nil
	})
	if len(nodes) == 0 {
		nodes = append(nodes, map[string]any{"id": app, "label": app, "file": "root_agent.yaml"})
	}
	return nodes, edges
}

type graphToolConfig struct {
	Name string `yaml:"name"`
}

type graphAgentConfig struct {
	Name        string            `yaml:"name"`
	DisplayName string            `yaml:"display_name"`
	AgentClass  string            `yaml:"agent_class"`
	Description string            `yaml:"description"`
	Model       string            `yaml:"model"`
	Skills      []string          `yaml:"skills"`
	Tools       []graphToolConfig `yaml:"tools"`
	Metadata    map[string]any    `yaml:"metadata"`
	SubAgents   []struct {
		ConfigPath string `yaml:"config_path"`
		Code       string `yaml:"code"`
	} `yaml:"sub_agents"`
}

func (c *BuilderAPIController) buildSemanticGraph(app, rootConfig string) ([]map[string]any, []map[string]any) {
	nodes := []map[string]any{}
	edges := []map[string]any{}
	seen := map[string]bool{}
	edgeSeen := map[string]bool{}

	var walk func(path string, parentID string, order int)
	walk = func(path string, parentID string, order int) {
		absPath, err := filepath.Abs(path)
		if err != nil || seen[absPath] {
			return
		}
		seen[absPath] = true

		cfg, err := readGraphAgentConfig(absPath)
		if err != nil {
			return
		}
		id := cfg.Name
		if id == "" {
			id = graphNodeIDFromPath(app, absPath, c.cfg.AppsRoot)
		}
		displayName := firstNonEmpty(
			cfg.DisplayName,
			stringFromMetadata(cfg.Metadata, "display_name"),
			stringFromMetadata(cfg.Metadata, "meta_name"),
			stringFromMetadata(cfg.Metadata, "name_zh"),
			stringFromMetadata(cfg.Metadata, "zh_name"),
		)
		labelName := firstNonEmpty(displayName, id)
		label := labelName
		if cfg.AgentClass != "" {
			label = fmt.Sprintf("%s\\n%s", labelName, cfg.AgentClass)
		}
		file := graphRelPath(absPath, c.cfg.AppsRoot)
		nodes = append(nodes, map[string]any{
			"id":          id,
			"label":       label,
			"displayName": displayName,
			"name":        cfg.Name,
			"agentClass":  cfg.AgentClass,
			"description": cfg.Description,
			"model":       cfg.Model,
			"skills":      cfg.Skills,
			"tools":       graphToolNames(cfg.Tools),
			"file":        file,
			"order":       order,
			"isRoot":      parentID == "",
			"isSubAgent":  parentID != "",
		})

		if parentID != "" {
			edgeKey := parentID + "\x00" + id
			if !edgeSeen[edgeKey] {
				edgeSeen[edgeKey] = true
				edges = append(edges, map[string]any{
					"from":  parentID,
					"to":    id,
					"order": order,
				})
			}
		}

		for i, ref := range cfg.SubAgents {
			if ref.ConfigPath == "" {
				continue
			}
			childPath := ref.ConfigPath
			if !filepath.IsAbs(childPath) {
				childPath = filepath.Join(filepath.Dir(absPath), childPath)
			}
			walk(childPath, id, i+1)
		}
	}

	walk(rootConfig, "", 0)
	if len(nodes) == 0 {
		return []map[string]any{{"id": app, "label": app, "file": "root_agent.yaml", "isRoot": true}}, nil
	}
	return nodes, edges
}

func graphToolNames(tools []graphToolConfig) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) != "" {
			out = append(out, tool.Name)
		}
	}
	return out
}

func readGraphAgentConfig(path string) (*graphAgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg graphAgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func graphNodeIDFromPath(app, absPath, appsRoot string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(absPath), ".yaml"), ".yml")
	if base == "root_agent" {
		return app
	}
	return base
}

func graphRelPath(absPath, appsRoot string) string {
	if appsRoot != "" {
		if rel, err := filepath.Rel(appsRoot, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(absPath)
}

func buildDOT(app string, nodes []map[string]any, edges []map[string]any) string {
	var b strings.Builder
	b.WriteString("digraph ")
	b.WriteString(dotQuote(app))
	b.WriteString(" {\n  rankdir=LR;\n")
	for _, node := range nodes {
		id, _ := node["id"].(string)
		label, _ := node["label"].(string)
		if label == "" {
			label = id
		}
		if id == "" {
			continue
		}
		b.WriteString("  ")
		b.WriteString(dotQuote(id))
		b.WriteString(" [label=")
		b.WriteString(dotQuote(label))
		b.WriteString("];\n")
	}
	for _, edge := range edges {
		from, _ := edge["from"].(string)
		to, _ := edge["to"].(string)
		if from == "" || to == "" {
			continue
		}
		b.WriteString("  ")
		b.WriteString(dotQuote(from))
		b.WriteString(" -> ")
		b.WriteString(dotQuote(to))
		b.WriteString(";\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func dotQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func (c *BuilderAPIController) targetRoot(req *http.Request) string {
	if req.URL.Query().Get("tmp") == "true" {
		return c.cfg.TmpRoot
	}
	return c.cfg.AppsRoot
}

func (c *BuilderAPIController) readNestedAppFileByBase(root, app, filePath string) ([]byte, bool, error) {
	if root == "" {
		return nil, false, nil
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
	base := filepath.Base(normalized)
	if base == "." || base == "" || base == string(filepath.Separator) {
		return nil, false, nil
	}
	if !strings.HasSuffix(base, ".yaml") && !strings.HasSuffix(base, ".yml") {
		return nil, false, nil
	}
	appRoot := filepath.Join(root, app)
	matches := []string{}
	if err := filepath.WalkDir(appRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == base {
			matches = append(matches, path)
		}
		return nil
	}); err != nil {
		return nil, false, nil
	}
	if len(matches) == 0 {
		return nil, false, nil
	}
	if len(matches) > 1 {
		return nil, false, fmt.Errorf("ambiguous builder app file %q in %s: %d matches", base, app, len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func cleanPathSegment(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, `/\\`) || s == "." || s == ".." {
		return "", fmt.Errorf("must be a single path segment")
	}
	return s, nil
}

func cleanBuilderRelPath(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = filepath.Clean(p)
	if p == "." || p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "..") || strings.Contains(p, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes builder root")
	}
	return p, nil
}

func extractBuilderAppName(data []byte) string {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return ""
	}
	name, _ := m["name"].(string)
	name = strings.TrimSpace(name)
	if _, err := cleanPathSegment(name); err != nil {
		return ""
	}
	return name
}

var legacyGeminiModelRE = regexp.MustCompile(`(?m)^(\s*model\s*:\s*)gemini[-_a-zA-Z0-9.]*\s*$`)

func rewriteBuilderDefaultModel(data []byte, defaultModel string) []byte {
	if strings.TrimSpace(defaultModel) == "" {
		defaultModel = "default"
	}
	return legacyGeminiModelRE.ReplaceAll(data, []byte(`${1}`+defaultModel))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
