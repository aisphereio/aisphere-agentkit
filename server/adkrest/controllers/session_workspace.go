package controllers

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultWorkspacePreviewBytes = 256 * 1024

type workspaceFileView struct {
	Path      string `json:"path"`
	Ref       string `json:"ref"`
	Bytes     int64  `json:"bytes"`
	ModTime   string `json:"mod_time"`
	Extension string `json:"extension"`
}

// SessionWorkspaceListHandler lists files in a session workspace. It is a
// read-only observability endpoint for manager/worker intermediate outputs.
func (c *RuntimeAPIController) SessionWorkspaceListHandler(rw http.ResponseWriter, req *http.Request) error {
	sessionID := strings.TrimSpace(req.URL.Query().Get("session_id"))
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	runID := strings.TrimSpace(req.URL.Query().Get("run_id"))
	base, err := c.sessionWorkspaceBase(sessionID, runID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(base); err != nil {
		if os.IsNotExist(err) {
			EncodeJSONResponse(map[string]any{"files": []workspaceFileView{}}, http.StatusOK, rw)
			return nil
		}
		return err
	}
	files := []workspaceFileView{}
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(c.sessionWorkspaceRoot(sessionID), path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		files = append(files, workspaceFileView{
			Path:      rel,
			Ref:       "workspace:session/" + workspaceSafeName(sessionID) + "/" + rel,
			Bytes:     info.Size(),
			ModTime:   info.ModTime().Format(time.RFC3339),
			Extension: strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), "."),
		})
		return nil
	})
	if err != nil {
		return err
	}
	EncodeJSONResponse(map[string]any{"files": files}, http.StatusOK, rw)
	return nil
}

// SessionWorkspaceReadHandler reads one bounded text or base64 preview from a
// session workspace file.
func (c *RuntimeAPIController) SessionWorkspaceReadHandler(rw http.ResponseWriter, req *http.Request) error {
	sessionID := strings.TrimSpace(req.URL.Query().Get("session_id"))
	relPath := strings.TrimSpace(req.URL.Query().Get("path"))
	if sessionID == "" || relPath == "" {
		return fmt.Errorf("session_id and path are required")
	}
	abs, err := c.safeSessionWorkspaceJoin(sessionID, relPath)
	if err != nil {
		return err
	}
	maxBytes := defaultWorkspacePreviewBytes
	if raw := strings.TrimSpace(req.URL.Query().Get("max_bytes")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < maxBytes {
			maxBytes = n
		}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	truncated := false
	if len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}
	text := ""
	base64Data := ""
	if isLikelyText(data) {
		text = string(data)
	} else {
		base64Data = base64.StdEncoding.EncodeToString(data)
	}
	EncodeJSONResponse(map[string]any{
		"path":       filepath.ToSlash(filepath.Clean(relPath)),
		"bytes":      len(data),
		"truncated":  truncated,
		"text":       text,
		"base64":     base64Data,
		"mime_type":  workspaceMimeType(relPath),
		"session_id": sessionID,
	}, http.StatusOK, rw)
	return nil
}

func (c *RuntimeAPIController) sessionWorkspaceBase(sessionID, runID string) (string, error) {
	base := c.sessionWorkspaceRoot(sessionID)
	if runID == "" {
		return base, nil
	}
	return safeWorkspaceJoin(base, filepath.Join("runs", runID))
}

func (c *RuntimeAPIController) sessionWorkspaceRoot(sessionID string) string {
	root := strings.TrimSpace(os.Getenv("ADK_SESSION_WORKSPACE_ROOT"))
	if root == "" && c.runtimeConfig != nil && c.runtimeConfig.Storage.Root != "" {
		root = filepath.Join(c.runtimeConfig.Storage.Root, "workspaces")
	}
	if root == "" {
		root = filepath.Join(".adk", "data", "workspaces")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return filepath.Join(abs, "sessions", workspaceSafeName(sessionID))
}

func (c *RuntimeAPIController) safeSessionWorkspaceJoin(sessionID, rel string) (string, error) {
	return safeWorkspaceJoin(c.sessionWorkspaceRoot(sessionID), rel)
}

func safeWorkspaceJoin(root, rel string) (string, error) {
	clean, err := cleanWorkspaceRel(rel)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(absRoot, clean)
	if abs != absRoot && !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved path escaped workspace")
	}
	return abs, nil
}

func cleanWorkspaceRel(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" || p == "." {
		return "", fmt.Errorf("path cannot be empty")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(p)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	return clean, nil
}

func workspaceSafeName(s string) string {
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
			b.WriteByte('_')
		}
	}
	return b.String()
}

func isLikelyText(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

func workspaceMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
