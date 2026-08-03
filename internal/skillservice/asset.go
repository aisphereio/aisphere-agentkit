package skillservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/tool/skilltoolset/skill"
	"gopkg.in/yaml.v3"
)

const (
	MetaFileName = ".skill-meta.json"

	StatusDraft      = "draft"
	StatusPublished  = "published"
	StatusDeprecated = "deprecated"
	StatusArchived   = "archived"
	StatusDeleted    = "deleted"

	VisibilityPrivate   = "private"
	VisibilityWorkspace = "workspace"
	VisibilityPublic    = "public"
	VisibilitySystem    = "system"
)

var ErrSkillInUse = errors.New("skill is referenced and cannot be physically deleted")

type PermissionGrant struct {
	SubjectType string   `json:"subjectType"`
	SubjectID   string   `json:"subjectId"`
	Permissions []string `json:"permissions"`
}

type Meta struct {
	OwnerType   string            `json:"ownerType,omitempty"`
	OwnerID     string            `json:"ownerId,omitempty"`
	Visibility  string            `json:"visibility,omitempty"`
	Status      string            `json:"status,omitempty"`
	CreatedBy   string            `json:"createdBy,omitempty"`
	UpdatedBy   string            `json:"updatedBy,omitempty"`
	PublishedBy string            `json:"publishedBy,omitempty"`
	ArchivedBy  string            `json:"archivedBy,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	UpdatedAt   string            `json:"updatedAt,omitempty"`
	PublishedAt string            `json:"publishedAt,omitempty"`
	ArchivedAt  string            `json:"archivedAt,omitempty"`
	DeletedAt   string            `json:"deletedAt,omitempty"`
	Permissions []PermissionGrant `json:"permissions,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

type DeleteOptions struct {
	Force       bool
	Physical    bool
	RequestedBy string
}

type ValidationIssue struct {
	Field    string `json:"field,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ValidationResult struct {
	OK       bool              `json:"ok"`
	Errors   []ValidationIssue `json:"errors"`
	Warnings []ValidationIssue `json:"warnings"`
}

type Reference struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
}

type ReferenceResult struct {
	Skill      string      `json:"skill"`
	References []Reference `json:"references"`
	Total      int         `json:"total"`
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending_review", "review":
		if strings.TrimSpace(status) == "" {
			return StatusDraft
		}
		return "pending_review"
	case StatusDraft, StatusPublished, StatusDeprecated, StatusArchived, StatusDeleted:
		return strings.ToLower(strings.TrimSpace(status))
	case "active":
		return StatusPublished
	case "yanked":
		return StatusArchived
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func isKnownStatus(status string) bool {
	switch normalizeStatus(status) {
	case StatusDraft, "pending_review", StatusPublished, StatusDeprecated, StatusArchived, StatusDeleted:
		return true
	default:
		return false
	}
}

func normalizeVisibility(visibility string) string {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "", "internal":
		if strings.TrimSpace(visibility) == "" {
			return VisibilityPrivate
		}
		return VisibilityWorkspace
	case VisibilityPrivate, VisibilityWorkspace, VisibilityPublic, VisibilitySystem:
		return strings.ToLower(strings.TrimSpace(visibility))
	default:
		return strings.ToLower(strings.TrimSpace(visibility))
	}
}

func isKnownVisibility(visibility string) bool {
	switch normalizeVisibility(visibility) {
	case VisibilityPrivate, VisibilityWorkspace, VisibilityPublic, VisibilitySystem:
		return true
	default:
		return false
	}
}

func (s *FileSystemService) metaPath(name string) string {
	return filepath.Join(s.root, name, MetaFileName)
}

func (s *FileSystemService) loadMeta(name string) Meta {
	m := Meta{Status: StatusDraft, Visibility: VisibilityPrivate, OwnerType: "system", OwnerID: "system"}
	data, err := os.ReadFile(s.metaPath(name))
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &m)
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = StatusDraft
	}
	if strings.TrimSpace(m.Visibility) == "" {
		m.Visibility = VisibilityPrivate
	}
	if strings.TrimSpace(m.OwnerType) == "" {
		m.OwnerType = "system"
	}
	if strings.TrimSpace(m.OwnerID) == "" {
		m.OwnerID = "system"
	}
	return m
}

func (s *FileSystemService) saveMeta(name string, m Meta) error {
	now := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(m.CreatedAt) == "" {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if strings.TrimSpace(m.Status) == "" {
		m.Status = StatusDraft
	}
	if strings.TrimSpace(m.Visibility) == "" {
		m.Visibility = VisibilityPrivate
	}
	if strings.TrimSpace(m.OwnerType) == "" {
		m.OwnerType = "system"
	}
	if strings.TrimSpace(m.OwnerID) == "" {
		m.OwnerID = "system"
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.metaPath(name), data, 0o644)
}

func mergeSummaryMeta(item *Summary, m Meta) {
	if item == nil {
		return
	}
	if item.Status == "" {
		item.Status = m.Status
	}
	if item.Visibility == "" {
		item.Visibility = m.Visibility
	}
	if item.Owner == "" {
		item.Owner = m.OwnerID
	}
	item.OwnerType = m.OwnerType
	item.OwnerID = m.OwnerID
	item.CreatedBy = m.CreatedBy
	item.UpdatedBy = m.UpdatedBy
	item.PublishedBy = m.PublishedBy
	item.ArchivedBy = m.ArchivedBy
	item.CreatedAt = m.CreatedAt
	if item.UpdatedAt == "" {
		item.UpdatedAt = m.UpdatedAt
	}
	item.PublishedAt = m.PublishedAt
	item.ArchivedAt = m.ArchivedAt
	item.DeletedAt = m.DeletedAt
	item.Permissions = m.Permissions
}

func updateMetaFromRequest(existing Meta, req SaveRequest) Meta {
	m := existing
	if m.Status == "" {
		m.Status = StatusDraft
	}
	if m.Visibility == "" {
		m.Visibility = VisibilityPrivate
	}
	if strings.TrimSpace(req.Status) != "" {
		m.Status = normalizeStatus(req.Status)
	}
	if strings.TrimSpace(req.Visibility) != "" {
		m.Visibility = normalizeVisibility(req.Visibility)
	}
	if strings.TrimSpace(req.Owner) != "" {
		m.OwnerID = strings.TrimSpace(req.Owner)
	}
	if strings.TrimSpace(m.OwnerType) == "" {
		m.OwnerType = "user"
	}
	if strings.TrimSpace(m.OwnerID) == "" {
		m.OwnerID = "system"
	}
	return m
}

func (s *FileSystemService) Validate(ctx context.Context, req SaveRequest) (*ValidationResult, error) {
	result := &ValidationResult{OK: true}
	addErr := func(field, msg string) {
		result.OK = false
		result.Errors = append(result.Errors, ValidationIssue{Field: field, Severity: "error", Message: msg})
	}
	addWarn := func(field, msg string) {
		result.Warnings = append(result.Warnings, ValidationIssue{Field: field, Severity: "warning", Message: msg})
	}

	name := strings.TrimSpace(req.Name)
	var fm *skill.Frontmatter
	instructions := req.Instructions
	if strings.TrimSpace(req.RawMarkdown) != "" {
		parsed, body, err := skill.ParseBytes([]byte(req.RawMarkdown))
		if err != nil {
			addErr("rawMarkdown", err.Error())
			return result, nil
		}
		fm = parsed
		instructions = body
		if name == "" {
			name = fm.Name
		}
		if name != fm.Name {
			addErr("name", fmt.Sprintf("request name %q does not match SKILL.md name %q", name, fm.Name))
		}
	} else {
		fm = &skill.Frontmatter{Name: name, Description: strings.TrimSpace(req.Description), License: strings.TrimSpace(req.License), Compatibility: strings.TrimSpace(req.Compatibility), Metadata: req.Metadata, AllowedTools: normalizeCSV(req.AllowedTools), Version: strings.TrimSpace(req.Version), Status: normalizeStatus(req.Status), Visibility: normalizeVisibility(req.Visibility), Category: strings.TrimSpace(req.Category), Owner: strings.TrimSpace(req.Owner), Changelog: strings.TrimSpace(req.Changelog), Labels: normalizeCSV(req.Labels), Tags: normalizeCSV(req.Tags)}
		if err := skill.Validate(fm); err != nil {
			addErr("frontmatter", err.Error())
		}
	}
	if strings.TrimSpace(instructions) == "" {
		addErr("instructions", "instructions body cannot be empty")
	}
	if fm != nil {
		if fm.Version == "" && req.Version == "" {
			addWarn("version", "version is empty; published skills should use semantic versions")
		}
		status := fm.Status
		if status == "" {
			status = req.Status
		}
		if status != "" && !isKnownStatus(status) {
			addErr("status", "unsupported status: "+status)
		}
		visibility := fm.Visibility
		if visibility == "" {
			visibility = req.Visibility
		}
		if visibility != "" && !isKnownVisibility(visibility) {
			addErr("visibility", "unsupported visibility: "+visibility)
		}
		for _, t := range fm.AllowedTools {
			if strings.TrimSpace(t) == "" {
				addErr("allowedTools", "allowed tool cannot be empty")
			}
		}
		if len(fm.AllowedTools) == 0 {
			addWarn("allowedTools", "no allowed-tools declared; this is fine for pure context skills but limits tool validation")
		}
	}
	return result, ctx.Err()
}

func (s *FileSystemService) ValidateExisting(ctx context.Context, name string) (*ValidationResult, error) {
	d, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.Validate(ctx, SaveRequest{Name: d.Name, RawMarkdown: d.RawMarkdown})
}

func (s *FileSystemService) References(ctx context.Context, name string) (*ReferenceResult, error) {
	if strings.TrimSpace(name) == "" {
		return nil, skill.ErrInvalidSkillName
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(s.root)
	refs := []Reference{}
	scanRoots := []struct{ typ, dir string }{{"agent", filepath.Join(repoRoot, "agents")}, {"flow", filepath.Join(repoRoot, "flows")}}
	for _, sr := range scanRoots {
		if _, err := os.Stat(sr.dir); err != nil {
			continue
		}
		err := filepath.WalkDir(sr.dir, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			if yamlSkillRefContains(data, name) {
				rel, _ := filepath.Rel(repoRoot, p)
				refs = append(refs, Reference{Type: sr.typ, Name: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), Path: filepath.ToSlash(rel)})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Type == refs[j].Type {
			return refs[i].Path < refs[j].Path
		}
		return refs[i].Type < refs[j].Type
	})
	return &ReferenceResult{Skill: name, References: refs, Total: len(refs)}, nil
}

func yamlSkillRefContains(data []byte, skillName string) bool {
	var root any
	if err := yaml.Unmarshal(data, &root); err == nil {
		return yamlAnyContainsSkill(root, skillName, false)
	}
	return strings.Contains(string(data), "- "+skillName) || strings.Contains(string(data), skillName)
}

func yamlAnyContainsSkill(v any, skillName string, underSkills bool) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if yamlAnyContainsSkill(val, skillName, underSkills || k == "skills") {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if yamlAnyContainsSkill(item, skillName, underSkills) {
				return true
			}
		}
	case string:
		return underSkills && x == skillName
	}
	return false
}

func (s *FileSystemService) DeleteWithOptions(ctx context.Context, name string, opts DeleteOptions) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return skill.ErrInvalidSkillName
	}
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return err
	}
	refs, err := s.References(ctx, name)
	if err != nil {
		return err
	}
	m := s.loadMeta(name)
	if len(refs.References) > 0 && !opts.Force {
		return fmt.Errorf("%w: %s is referenced by %d asset(s)", ErrSkillInUse, name, len(refs.References))
	}
	if opts.Physical && (m.Status == StatusDraft || opts.Force) {
		dir := filepath.Join(s.root, name)
		if !isWithinRoot(s.root, dir) {
			return skill.ErrInvalidSkillName
		}
		return os.RemoveAll(dir)
	}
	now := time.Now().Format(time.RFC3339)
	m.Status = StatusDeleted
	m.DeletedAt = now
	if opts.RequestedBy != "" {
		m.UpdatedBy = opts.RequestedBy
	}
	return s.saveMeta(name, m)
}

func (s *FileSystemService) UpdateStatus(ctx context.Context, name, status, actor string) (*Detail, error) {
	if _, err := s.baseSource().LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	m := s.loadMeta(name)
	normalized := normalizeStatus(status)
	if !isKnownStatus(normalized) {
		return nil, fmt.Errorf("unsupported status %q", status)
	}
	m.Status = normalized
	if actor != "" {
		m.UpdatedBy = actor
	}
	now := time.Now().Format(time.RFC3339)
	switch normalized {
	case StatusPublished:
		m.PublishedAt = now
		if actor != "" {
			m.PublishedBy = actor
		}
	case StatusArchived:
		m.ArchivedAt = now
		if actor != "" {
			m.ArchivedBy = actor
		}
	}
	if err := s.saveMeta(name, m); err != nil {
		return nil, err
	}
	return s.Get(ctx, name)
}

func defaultPermissionsFor(ownerID string) []PermissionGrant {
	if strings.TrimSpace(ownerID) == "" {
		ownerID = "system"
	}
	return []PermissionGrant{{SubjectType: "user", SubjectID: ownerID, Permissions: []string{"read", "use", "write", "publish", "delete", "admin"}}, {SubjectType: "role", SubjectID: "admin", Permissions: []string{"read", "use", "write", "publish", "delete", "admin"}}}
}
