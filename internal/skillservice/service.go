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

// Package skillservice provides platform-level skill management backed by the
// ADK skill.Source abstraction. It intentionally keeps skills as file-system
// assets so the runtime can load SKILL.md files without coupling business logic
// to a database. Admin/SkillHub-like fields are stored in SKILL.md metadata to
// keep the repository portable and OpenSkills-compatible.
package skillservice

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/tool/skilltoolset/skill"
)

const (
	SkillFileName        = "SKILL.md"
	maxImportPackageSize = 50 * 1024 * 1024
	maxResourceWriteSize = 10 * 1024 * 1024
)

type Summary struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName,omitempty"`
	DisplayNameSnake string            `json:"display_name,omitempty"`
	Description      string            `json:"description"`
	License          string            `json:"license,omitempty"`
	Compatibility    string            `json:"compatibility,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	AllowedTools     []string          `json:"allowedTools,omitempty"`

	// SkillHub-inspired admin fields. They are derived from metadata keys so the
	// source of truth remains the SKILL.md frontmatter.
	Version    string   `json:"version,omitempty"`
	Status     string   `json:"status,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	Category   string   `json:"category,omitempty"`
	Owner      string   `json:"owner,omitempty"`
	Changelog  string   `json:"changelog,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	UpdatedAt  string   `json:"updatedAt,omitempty"`

	// Platform metadata stored in .skill-meta.json. This is deliberately separate
	// from SKILL.md content so permissions/lifecycle can evolve without rewriting
	// the skill instruction body.
	OwnerType   string            `json:"ownerType,omitempty"`
	OwnerID     string            `json:"ownerId,omitempty"`
	CreatedBy   string            `json:"createdBy,omitempty"`
	UpdatedBy   string            `json:"updatedBy,omitempty"`
	PublishedBy string            `json:"publishedBy,omitempty"`
	ArchivedBy  string            `json:"archivedBy,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	PublishedAt string            `json:"publishedAt,omitempty"`
	ArchivedAt  string            `json:"archivedAt,omitempty"`
	DeletedAt   string            `json:"deletedAt,omitempty"`
	Permissions []PermissionGrant `json:"permissions,omitempty"`
}

type Detail struct {
	Summary
	Instructions string   `json:"instructions"`
	RawMarkdown  string   `json:"rawMarkdown,omitempty"`
	Resources    []string `json:"resources,omitempty"`
}

type SaveRequest struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName,omitempty"`
	DisplayNameSnake string            `json:"display_name,omitempty"`
	Description      string            `json:"description"`
	License          string            `json:"license,omitempty"`
	Compatibility    string            `json:"compatibility,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	AllowedTools     []string          `json:"allowedTools,omitempty"`
	Instructions     string            `json:"instructions"`
	RawMarkdown      string            `json:"rawMarkdown,omitempty"`

	// Optional admin fields mapped to metadata.
	Version    string   `json:"version,omitempty"`
	Status     string   `json:"status,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	Category   string   `json:"category,omitempty"`
	Owner      string   `json:"owner,omitempty"`
	Changelog  string   `json:"changelog,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

type SaveResourceRequest struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty"` // empty/plain/base64
}

type Service interface {
	Root() string
	Source(ctx context.Context, selected []string, preload string) (skill.Source, error)
	List(ctx context.Context) ([]Summary, error)
	Get(ctx context.Context, name string) (*Detail, error)
	Save(ctx context.Context, req SaveRequest) (*Detail, error)
	Delete(ctx context.Context, name string) error
	DeleteWithOptions(ctx context.Context, name string, opts DeleteOptions) error
	UpdateStatus(ctx context.Context, name, status, actor string) (*Detail, error)
	Validate(ctx context.Context, req SaveRequest) (*ValidationResult, error)
	ValidateExisting(ctx context.Context, name string) (*ValidationResult, error)
	References(ctx context.Context, name string) (*ReferenceResult, error)
	ListResources(ctx context.Context, name, subpath string) ([]string, error)
	LoadResource(ctx context.Context, name, resourcePath string) ([]byte, error)
	SaveResource(ctx context.Context, name, resourcePath string, data []byte) error
	DeleteResource(ctx context.Context, name, resourcePath string) error
	ExportPackage(ctx context.Context, name string) ([]byte, string, error)
	ImportPackage(ctx context.Context, filename string, data []byte) (*Detail, error)
}

type FileSystemService struct {
	root string
}

func NewFileSystemService(root string) (*FileSystemService, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("skill root cannot be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create skill root %q: %w", abs, err)
	}
	return &FileSystemService{root: abs}, nil
}

func (s *FileSystemService) Root() string { return s.root }

func (s *FileSystemService) baseSource() skill.Source {
	return skill.NewFileSystemSource(os.DirFS(s.root))
}

func (s *FileSystemService) Source(ctx context.Context, selected []string, preload string) (skill.Source, error) {
	var source skill.Source = s.baseSource()
	preload = strings.ToLower(strings.TrimSpace(preload))
	switch preload {
	case "", "none", "off", "false":
		// no preload
	case "frontmatter", "frontmatters", "metadata":
		var err error
		source, _, err = skill.WithFrontmatterPreloadSource(ctx, source)
		if err != nil {
			return nil, err
		}
	case "complete", "all", "full":
		var err error
		source, _, err = skill.WithCompletePreloadSource(ctx, source)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported skill preload mode %q", preload)
	}
	selected = normalizeSelected(selected)
	if len(selected) == 0 || contains(selected, "*") {
		return source, nil
	}
	return NewFilteredSource(source, selected), nil
}

func (s *FileSystemService) List(ctx context.Context) ([]Summary, error) {
	fms, err := s.baseSource().ListFrontmatters(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(fms))
	for _, fm := range fms {
		item := summaryFromFrontmatter(fm)
		mergeSummaryMeta(&item, s.loadMeta(item.Name))
		if item.Status == StatusDeleted || item.DeletedAt != "" {
			continue
		}
		if info, statErr := os.Stat(filepath.Join(s.root, item.Name, SkillFileName)); statErr == nil {
			item.UpdatedAt = info.ModTime().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *FileSystemService) Get(ctx context.Context, name string) (*Detail, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, skill.ErrInvalidSkillName
	}
	source := s.baseSource()
	fm, err := source.LoadFrontmatter(ctx, name)
	if err != nil {
		return nil, err
	}
	instructions, err := source.LoadInstructions(ctx, name)
	if err != nil {
		return nil, err
	}
	resources, _ := source.ListResources(ctx, name, ".")
	raw, _ := os.ReadFile(filepath.Join(s.root, name, SkillFileName))
	summary := summaryFromFrontmatter(fm)
	mergeSummaryMeta(&summary, s.loadMeta(name))
	if summary.Status == StatusDeleted || summary.DeletedAt != "" {
		return nil, skill.ErrSkillNotFound
	}
	if info, statErr := os.Stat(filepath.Join(s.root, name, SkillFileName)); statErr == nil {
		summary.UpdatedAt = info.ModTime().Format(time.RFC3339)
	}
	return &Detail{Summary: summary, Instructions: instructions, RawMarkdown: string(raw), Resources: resources}, nil
}

func (s *FileSystemService) Save(ctx context.Context, req SaveRequest) (*Detail, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" && req.RawMarkdown != "" {
		fm, _, err := skill.ParseBytes([]byte(req.RawMarkdown))
		if err != nil {
			return nil, err
		}
		name = fm.Name
	}
	if name == "" {
		return nil, fmt.Errorf("skill name is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var data []byte
	var err error
	if strings.TrimSpace(req.RawMarkdown) != "" {
		fm, _, err := skill.ParseBytes([]byte(req.RawMarkdown))
		if err != nil {
			return nil, err
		}
		if fm.Name != name {
			return nil, fmt.Errorf("raw SKILL.md name %q does not match request name %q", fm.Name, name)
		}
		data = []byte(req.RawMarkdown)
	} else {
		metadata := copyMetadata(req.Metadata)
		if displayName := firstNonEmpty(req.DisplayName, req.DisplayNameSnake); displayName != "" {
			metadata["display_name"] = displayName
		}

		fm := &skill.Frontmatter{
			Name:          name,
			Description:   strings.TrimSpace(req.Description),
			License:       strings.TrimSpace(req.License),
			Compatibility: strings.TrimSpace(req.Compatibility),
			Version:       strings.TrimSpace(req.Version),
			Status:        normalizeStatus(req.Status),
			Visibility:    normalizeVisibility(req.Visibility),
			Category:      strings.TrimSpace(req.Category),
			Owner:         strings.TrimSpace(req.Owner),
			Changelog:     strings.TrimSpace(req.Changelog),
			Labels:        normalizeCSV(req.Labels),
			Tags:          normalizeCSV(req.Tags),
			Metadata:      emptyToNil(metadata),
			AllowedTools:  normalizeCSV(req.AllowedTools),
		}
		data, err = skill.Build(fm, strings.TrimLeft(req.Instructions, "\r\n"))
		if err != nil {
			return nil, err
		}
	}

	dir := filepath.Join(s.root, name)
	if !isWithinRoot(s.root, dir) {
		return nil, skill.ErrInvalidSkillName
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create skill directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillFileName), data, 0o644); err != nil {
		return nil, fmt.Errorf("write SKILL.md: %w", err)
	}
	meta := updateMetaFromRequest(s.loadMeta(name), req)
	if meta.OwnerID == "system" && strings.TrimSpace(req.Owner) == "" {
		meta.Permissions = defaultPermissionsFor(meta.OwnerID)
	} else if len(meta.Permissions) == 0 {
		meta.Permissions = defaultPermissionsFor(meta.OwnerID)
	}
	if err := s.saveMeta(name, meta); err != nil {
		return nil, fmt.Errorf("write %s: %w", MetaFileName, err)
	}
	return s.Get(ctx, name)
}

func (s *FileSystemService) Delete(ctx context.Context, name string) error {
	return s.DeleteWithOptions(ctx, name, DeleteOptions{})
}

func (s *FileSystemService) ListResources(ctx context.Context, name, subpath string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	skillDir := filepath.Join(s.root, name)
	if !isWithinRoot(s.root, skillDir) {
		return nil, skill.ErrInvalidSkillName
	}
	baseDir := skillDir
	if strings.TrimSpace(subpath) != "" && subpath != "." {
		clean, err := safeResourcePath(subpath)
		if err != nil {
			return nil, err
		}
		baseDir = filepath.Join(skillDir, filepath.FromSlash(clean))
		if !isWithinRoot(skillDir, baseDir) {
			return nil, skill.ErrInvalidResourcePath
		}
	}
	if _, err := os.Stat(baseDir); err != nil {
		if os.IsNotExist(err) {
			return nil, skill.ErrResourceNotFound
		}
		return nil, err
	}
	resources := []string{}
	err := filepath.WalkDir(baseDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == SkillFileName {
			return nil
		}
		resources = append(resources, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(resources)
	return resources, nil
}

func (s *FileSystemService) LoadResource(ctx context.Context, name, resourcePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	clean, err := safeResourcePath(resourcePath)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(clean, SkillFileName) {
		return nil, skill.ErrInvalidResourcePath
	}
	skillDir := filepath.Join(s.root, name)
	target := filepath.Join(skillDir, filepath.FromSlash(clean))
	if !isWithinRoot(skillDir, target) {
		return nil, skill.ErrInvalidResourcePath
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, skill.ErrResourceNotFound
		}
		return nil, err
	}
	return data, nil
}

func (s *FileSystemService) SaveResource(ctx context.Context, name, resourcePath string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return err
	}
	if len(data) > maxResourceWriteSize {
		return fmt.Errorf("resource exceeds %d bytes limit", maxResourceWriteSize)
	}
	clean, err := safeResourcePath(resourcePath)
	if err != nil {
		return err
	}
	if strings.EqualFold(clean, SkillFileName) {
		return fmt.Errorf("use skill save API to modify %s", SkillFileName)
	}
	target := filepath.Join(s.root, name, filepath.FromSlash(clean))
	if !isWithinRoot(filepath.Join(s.root, name), target) {
		return skill.ErrInvalidResourcePath
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create resource directory: %w", err)
	}
	return os.WriteFile(target, data, 0o644)
}

func (s *FileSystemService) DeleteResource(ctx context.Context, name, resourcePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return err
	}
	clean, err := safeResourcePath(resourcePath)
	if err != nil {
		return err
	}
	if strings.EqualFold(clean, SkillFileName) {
		return fmt.Errorf("%s cannot be deleted through resource API", SkillFileName)
	}
	target := filepath.Join(s.root, name, filepath.FromSlash(clean))
	if !isWithinRoot(filepath.Join(s.root, name), target) {
		return skill.ErrInvalidResourcePath
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			return skill.ErrResourceNotFound
		}
		return err
	}
	pruneEmptyParents(filepath.Join(s.root, name), filepath.Dir(target))
	return nil
}

func (s *FileSystemService) ExportPackage(ctx context.Context, name string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return nil, "", err
	}
	skillDir := filepath.Join(s.root, name)
	if !isWithinRoot(s.root, skillDir) {
		return nil, "", skill.ErrInvalidSkillName
	}

	buf := bytes.Buffer{}
	zw := zip.NewWriter(&buf)
	if err := filepath.WalkDir(skillDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, p)
		if err != nil {
			return err
		}
		entryName := path.Join(name, filepath.ToSlash(rel))
		w, err := zw.Create(entryName)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}); err != nil {
		_ = zw.Close()
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), name + ".zip", nil
}

func (s *FileSystemService) ImportPackage(ctx context.Context, filename string, data []byte) (*Detail, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty package")
	}
	if len(data) > maxImportPackageSize {
		return nil, fmt.Errorf("package exceeds %d bytes limit", maxImportPackageSize)
	}
	if !looksLikeZip(filename, data) {
		fm, _, err := skill.ParseBytes(data)
		if err != nil {
			return nil, fmt.Errorf("package must be a zip or SKILL.md: %w", err)
		}
		return s.Save(ctx, SaveRequest{Name: fm.Name, RawMarkdown: string(data)})
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	skillFile, basePrefix, err := findSkillFileInZip(zr)
	if err != nil {
		return nil, err
	}
	r, err := skillFile.Open()
	if err != nil {
		return nil, err
	}
	rawSkill, err := io.ReadAll(io.LimitReader(r, maxResourceWriteSize+1))
	_ = r.Close()
	if err != nil {
		return nil, err
	}
	fm, _, err := skill.ParseBytes(rawSkill)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", skillFile.Name, err)
	}
	name := fm.Name
	dir := filepath.Join(s.root, name)
	if !isWithinRoot(s.root, dir) {
		return nil, skill.ErrInvalidSkillName
	}

	tmpDir, err := os.MkdirTemp(s.root, ".import-"+name+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	writtenSkill := false
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entryName := filepath.ToSlash(f.Name)
		if f.FileInfo().IsDir() || shouldSkipZipEntry(entryName) {
			continue
		}
		if basePrefix != "" && entryName != basePrefix && !strings.HasPrefix(entryName, basePrefix+"/") {
			continue
		}
		rel := strings.TrimPrefix(entryName, basePrefix)
		rel = strings.TrimPrefix(rel, "/")
		clean, err := safeImportPath(rel)
		if err != nil {
			return nil, fmt.Errorf("invalid zip entry %q: %w", f.Name, err)
		}
		in, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(in, maxResourceWriteSize+1))
		_ = in.Close()
		if err != nil {
			return nil, err
		}
		if len(content) > maxResourceWriteSize {
			return nil, fmt.Errorf("zip entry %q exceeds %d bytes limit", f.Name, maxResourceWriteSize)
		}
		if clean == SkillFileName {
			if _, _, err := skill.ParseBytes(content); err != nil {
				return nil, fmt.Errorf("parse imported SKILL.md: %w", err)
			}
			writtenSkill = true
		}
		target := filepath.Join(tmpDir, filepath.FromSlash(clean))
		if !isWithinRoot(tmpDir, target) {
			return nil, skill.ErrInvalidResourcePath
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return nil, err
		}
	}
	if !writtenSkill {
		return nil, fmt.Errorf("zip package does not contain %s", SkillFileName)
	}

	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpDir, dir); err != nil {
		return nil, err
	}
	meta := s.loadMeta(name)
	if len(meta.Permissions) == 0 {
		meta.Permissions = defaultPermissionsFor(meta.OwnerID)
	}
	if err := s.saveMeta(name, meta); err != nil {
		return nil, err
	}
	return s.Get(ctx, name)
}

func summaryFromFrontmatter(fm *skill.Frontmatter) Summary {
	if fm == nil {
		return Summary{}
	}
	metadata := copyMetadata(fm.Metadata)
	version := firstNonEmpty(fm.Version, metadata["version"])
	status := firstNonEmpty(fm.Status, metadata["status"])
	visibility := firstNonEmpty(fm.Visibility, metadata["visibility"])
	category := firstNonEmpty(fm.Category, metadata["category"])
	owner := firstNonEmpty(fm.Owner, metadata["owner"])
	changelog := firstNonEmpty(fm.Changelog, metadata["changelog"])
	labels := fm.Labels
	if len(labels) == 0 {
		labels = splitCSV(metadata["labels"])
	}
	tags := fm.Tags
	if len(tags) == 0 {
		tags = splitCSV(metadata["tags"])
	}
	displayName := firstNonEmpty(metadata["display_name"], metadata["displayName"], metadata["title"])
	return Summary{
		Name:             fm.Name,
		DisplayName:      displayName,
		DisplayNameSnake: displayName,
		Description:      fm.Description,
		License:          fm.License,
		Compatibility:    fm.Compatibility,
		Metadata:         emptyToNil(metadata),
		AllowedTools:     fm.AllowedTools,
		Version:          version,
		Status:           normalizeStatus(status),
		Visibility:       normalizeVisibility(visibility),
		Category:         category,
		Owner:            owner,
		Changelog:        changelog,
		Labels:           labels,
		Tags:             tags,
	}
}

func DecodeResourceContent(req SaveResourceRequest) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(req.Encoding)) {
	case "", "plain", "text", "utf8", "utf-8":
		return []byte(req.Content), nil
	case "base64":
		return base64.StdEncoding.DecodeString(req.Content)
	default:
		return nil, fmt.Errorf("unsupported resource encoding %q", req.Encoding)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func normalizeSelected(selected []string) []string {
	out := make([]string, 0, len(selected))
	seen := map[string]bool{}
	for _, s := range selected {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func isWithinRoot(root, candidate string) bool {
	root, _ = filepath.Abs(root)
	candidate, _ = filepath.Abs(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func respondableError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ResourcePathForURL normalizes URL style paths for resource APIs.
func ResourcePathForURL(raw string) string {
	clean := path.Clean(strings.TrimPrefix(raw, "/"))
	if clean == "." {
		return ""
	}
	return clean
}

func copyMetadata(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

func putMetadata(metadata map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	metadata[key] = value
}

func emptyToNil(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func splitCSV(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '\n' || r == '\t' })
	return normalizeCSV(parts)
}

func normalizeCSV(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '\n' || r == '\t' }) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func safeResourcePath(resourcePath string) (string, error) {
	resourcePath = strings.TrimSpace(strings.ReplaceAll(resourcePath, "\\", "/"))
	if resourcePath == "" {
		return "", skill.ErrInvalidResourcePath
	}
	clean := path.Clean(strings.TrimPrefix(resourcePath, "/"))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." || path.IsAbs(clean) {
		return "", skill.ErrInvalidResourcePath
	}
	return clean, nil
}

func safeImportPath(resourcePath string) (string, error) {
	clean, err := safeResourcePath(resourcePath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(clean, ".git/") || strings.HasPrefix(clean, "__MACOSX/") {
		return "", skill.ErrInvalidResourcePath
	}
	return clean, nil
}

func pruneEmptyParents(root, dir string) {
	root, _ = filepath.Abs(root)
	for {
		dir, _ = filepath.Abs(dir)
		if dir == root || !isWithinRoot(root, dir) {
			return
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}

func looksLikeZip(filename string, data []byte) bool {
	return strings.HasSuffix(strings.ToLower(filename), ".zip") || (len(data) >= 4 && string(data[:4]) == "PK\x03\x04")
}

func findSkillFileInZip(zr *zip.Reader) (*zip.File, string, error) {
	var candidate *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || shouldSkipZipEntry(f.Name) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(f.Name), "/")
		if len(parts) == 0 || parts[len(parts)-1] != SkillFileName {
			continue
		}
		if candidate == nil || len(parts) < len(strings.Split(filepath.ToSlash(candidate.Name), "/")) {
			candidate = f
		}
	}
	if candidate == nil {
		return nil, "", fmt.Errorf("zip package does not contain %s", SkillFileName)
	}
	basePrefix := path.Dir(filepath.ToSlash(candidate.Name))
	if basePrefix == "." {
		basePrefix = ""
	}
	return candidate, basePrefix, nil
}

func shouldSkipZipEntry(name string) bool {
	name = filepath.ToSlash(name)
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") || base == ".DS_Store" || base == "Thumbs.db"
}
