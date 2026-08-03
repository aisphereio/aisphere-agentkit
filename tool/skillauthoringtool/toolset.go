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

// Package skillauthoringtool exposes guarded tools for turning reviewed
// research artifacts into real filesystem-backed ADK Skill drafts.
package skillauthoringtool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/skillservice"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	adkskill "google.golang.org/adk/tool/skilltoolset/skill"
)

const (
	defaultCategory   = "novel-writing"
	defaultVersion    = "0.1.0"
	defaultVisibility = skillservice.VisibilityWorkspace
	defaultStatus     = "pending_review"
)

var generatedSkillNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,79}$`)

// NewToolset creates the Skill authoring toolset.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "skill_validate_draft",
				Description: "Validate a proposed ADK Skill draft without saving it. Use this before writing model-generated skill drafts.",
			}, ts.ValidateDraft)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "skill_save_draft",
				Description: "Save a proposed ADK Skill as a real draft/pending-review Skill in the configured skills repository. This does not publish it for production use.",
			}, ts.SaveDraft)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "skill_get_draft",
				Description: "Read a real Skill draft/detail from the configured skills repository by skill name.",
			}, ts.GetDraft)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "skill_list_drafts",
				Description: "List real Skills in the configured skills repository, optionally filtered by query/status/category.",
			}, ts.ListDrafts)
		},
	}
	for _, build := range builders {
		tl, err := build()
		if err != nil {
			return nil, err
		}
		ts.tools = append(ts.tools, tl)
	}
	return ts, nil
}

// Toolset groups skill-authoring tools.
type Toolset struct {
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "SkillAuthoringToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return t.tools, nil
}

// SkillDraftArgs is the input shape for validating/saving generated skills.
type SkillDraftArgs struct {
	Name             string            `json:"name" jsonschema:"Stable skill name, lowercase kebab-case, for example novel-dialogue-power-gap. Must not contain spaces or underscores."`
	Description      string            `json:"description" jsonschema:"One-sentence description of what this skill teaches."`
	Instructions     string            `json:"instructions,omitempty" jsonschema:"SKILL.md body without frontmatter. Required unless raw_markdown is provided."`
	RawMarkdown      string            `json:"raw_markdown,omitempty" jsonschema:"Full SKILL.md including YAML frontmatter. If provided, it is parsed and must match name."`
	Version          string            `json:"version,omitempty" jsonschema:"Semantic version. Defaults to 0.1.0 for generated drafts."`
	Category         string            `json:"category,omitempty" jsonschema:"Skill category. Defaults to novel-writing."`
	Visibility       string            `json:"visibility,omitempty" jsonschema:"private/workspace/public. Defaults to workspace. Generated drafts cannot use system visibility."`
	Status           string            `json:"status,omitempty" jsonschema:"draft or pending_review. Generated tools never publish directly."`
	Owner            string            `json:"owner,omitempty" jsonschema:"Optional owner id. Defaults to current user id."`
	Labels           []string          `json:"labels,omitempty" jsonschema:"Human-facing labels."`
	Tags             []string          `json:"tags,omitempty" jsonschema:"Search/filter tags."`
	AllowedTools     []string          `json:"allowed_tools,omitempty" jsonschema:"Allowed tool names. Usually empty for pure writing-method skills."`
	Metadata         map[string]string `json:"metadata,omitempty" jsonschema:"Extra metadata such as source book id, source artifact names, evidence chapters, gap decision."`
	SourceArtifacts  []string          `json:"source_artifacts,omitempty" jsonschema:"Artifact ids used as evidence, for traceability."`
	EvidenceChapters []string          `json:"evidence_chapters,omitempty" jsonschema:"Chapter numbers/titles used as evidence."`
	Overwrite        bool              `json:"overwrite,omitempty" jsonschema:"Whether to overwrite an existing skill with the same name. Defaults to false."`
}

type SkillNameArgs struct {
	Name string `json:"name" jsonschema:"Skill name."`
}

type ListDraftsArgs struct {
	Query    string `json:"query,omitempty" jsonschema:"Optional substring filter over name, description, category, tags, labels."`
	Status   string `json:"status,omitempty" jsonschema:"Optional status filter, e.g. draft, pending_review, published."`
	Category string `json:"category,omitempty" jsonschema:"Optional category filter."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of skills to return. Defaults to 50."`
}

type SkillDraftResult struct {
	Name             string                         `json:"name"`
	Description      string                         `json:"description"`
	Version          string                         `json:"version,omitempty"`
	Status           string                         `json:"status"`
	Visibility       string                         `json:"visibility"`
	Category         string                         `json:"category,omitempty"`
	Owner            string                         `json:"owner,omitempty"`
	Path             string                         `json:"path,omitempty"`
	Validation       *skillservice.ValidationResult `json:"validation"`
	QualityGate      QualityGateResult              `json:"quality_gate"`
	SourceArtifacts  []string                       `json:"source_artifacts,omitempty"`
	EvidenceChapters []string                       `json:"evidence_chapters,omitempty"`
}

type SkillDetailResult struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version,omitempty"`
	Status       string   `json:"status"`
	Visibility   string   `json:"visibility"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Instructions string   `json:"instructions"`
	RawMarkdown  string   `json:"raw_markdown,omitempty"`
	Resources    []string `json:"resources,omitempty"`
}

type SkillListResult struct {
	Count  int                    `json:"count"`
	Skills []skillservice.Summary `json:"skills"`
}

type QualityGateResult struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func (t *Toolset) ValidateDraft(ctx tool.Context, args SkillDraftArgs) (SkillDraftResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return SkillDraftResult{}, err
	}
	req, qg, err := buildSaveRequest(ctx, args)
	if err != nil {
		return SkillDraftResult{}, err
	}
	validation, err := svc.Validate(ctx, req)
	if err != nil {
		return SkillDraftResult{}, err
	}
	if !qg.OK {
		validation.OK = false
		for _, msg := range qg.Errors {
			validation.Errors = append(validation.Errors, skillservice.ValidationIssue{Field: "quality_gate", Severity: "error", Message: msg})
		}
	}
	return SkillDraftResult{
		Name:             req.Name,
		Description:      req.Description,
		Version:          req.Version,
		Status:           req.Status,
		Visibility:       req.Visibility,
		Category:         req.Category,
		Owner:            req.Owner,
		Validation:       validation,
		QualityGate:      qg,
		SourceArtifacts:  args.SourceArtifacts,
		EvidenceChapters: args.EvidenceChapters,
	}, nil
}

func (t *Toolset) SaveDraft(ctx tool.Context, args SkillDraftArgs) (SkillDraftResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return SkillDraftResult{}, err
	}
	req, qg, err := buildSaveRequest(ctx, args)
	if err != nil {
		return SkillDraftResult{}, err
	}
	if !qg.OK {
		return SkillDraftResult{}, fmt.Errorf("skill draft failed quality gate: %s", strings.Join(qg.Errors, "; "))
	}
	if !args.Overwrite {
		if _, err := svc.Get(ctx, req.Name); err == nil {
			return SkillDraftResult{}, fmt.Errorf("skill %q already exists; pass overwrite=true only after explicitly deciding to replace it", req.Name)
		} else if !errors.Is(err, adkskill.ErrSkillNotFound) {
			return SkillDraftResult{}, fmt.Errorf("check existing skill %q: %w", req.Name, err)
		}
	}
	validation, err := svc.Validate(ctx, req)
	if err != nil {
		return SkillDraftResult{}, err
	}
	if !validation.OK {
		return SkillDraftResult{}, fmt.Errorf("skill draft validation failed: %s", joinValidationErrors(validation))
	}
	detail, err := svc.Save(ctx, req)
	if err != nil {
		return SkillDraftResult{}, err
	}
	return SkillDraftResult{
		Name:             detail.Name,
		Description:      detail.Description,
		Version:          detail.Version,
		Status:           detail.Status,
		Visibility:       detail.Visibility,
		Category:         detail.Category,
		Owner:            detail.Owner,
		Path:             svc.Root() + "/" + detail.Name + "/SKILL.md",
		Validation:       validation,
		QualityGate:      qg,
		SourceArtifacts:  args.SourceArtifacts,
		EvidenceChapters: args.EvidenceChapters,
	}, nil
}

func (t *Toolset) GetDraft(ctx tool.Context, args SkillNameArgs) (SkillDetailResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return SkillDetailResult{}, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return SkillDetailResult{}, fmt.Errorf("name is required")
	}
	d, err := svc.Get(ctx, name)
	if err != nil {
		return SkillDetailResult{}, err
	}
	return SkillDetailResult{
		Name:         d.Name,
		Description:  d.Description,
		Version:      d.Version,
		Status:       d.Status,
		Visibility:   d.Visibility,
		Category:     d.Category,
		Tags:         d.Tags,
		Labels:       d.Labels,
		Instructions: d.Instructions,
		RawMarkdown:  d.RawMarkdown,
		Resources:    d.Resources,
	}, nil
}

func (t *Toolset) ListDrafts(ctx tool.Context, args ListDraftsArgs) (SkillListResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return SkillListResult{}, err
	}
	items, err := svc.List(ctx)
	if err != nil {
		return SkillListResult{}, err
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	status := strings.ToLower(strings.TrimSpace(args.Status))
	category := strings.ToLower(strings.TrimSpace(args.Category))
	filtered := make([]skillservice.Summary, 0, len(items))
	for _, item := range items {
		if status != "" && strings.ToLower(item.Status) != status {
			continue
		}
		if category != "" && strings.ToLower(item.Category) != category {
			continue
		}
		if query != "" && !summaryContains(item, query) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return SkillListResult{Count: len(filtered), Skills: filtered}, nil
}

func serviceFromContext(ctx context.Context) (skillservice.Service, error) {
	cfg := runtimeconfig.FromContext(ctx)
	if !cfg.Skills.Enabled {
		return nil, fmt.Errorf("skills are disabled in runtime config")
	}
	if strings.TrimSpace(cfg.Skills.Root) == "" {
		return nil, fmt.Errorf("skills.root is empty in runtime config")
	}
	return skillservice.NewFileSystemService(cfg.Skills.Root)
}

func buildSaveRequest(ctx tool.Context, args SkillDraftArgs) (skillservice.SaveRequest, QualityGateResult, error) {
	if strings.TrimSpace(args.RawMarkdown) != "" {
		fm, body, err := adkskill.ParseBytes([]byte(args.RawMarkdown))
		if err != nil {
			return skillservice.SaveRequest{}, QualityGateResult{}, err
		}
		name := strings.TrimSpace(args.Name)
		if name == "" {
			name = fm.Name
		}
		if name != fm.Name {
			return skillservice.SaveRequest{}, QualityGateResult{}, fmt.Errorf("raw_markdown skill name %q does not match args name %q", fm.Name, name)
		}
		qg := qualityGate(name, fm.Description, body)
		metadata := copyMetadata(args.Metadata)
		addGeneratedMetadata(metadata, args)
		// Keep raw markdown as the model wrote it, but persist traceability in
		// sidecar meta through request fields when possible.
		return skillservice.SaveRequest{
			Name:        name,
			RawMarkdown: args.RawMarkdown,
			Status:      safeDraftStatus(firstNonEmpty(args.Status, fm.Status)),
			Visibility:  safeVisibility(firstNonEmpty(args.Visibility, fm.Visibility)),
			Owner:       firstNonEmpty(args.Owner, ctx.UserID()),
			Metadata:    metadata,
		}, qg, nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return skillservice.SaveRequest{}, QualityGateResult{}, fmt.Errorf("name is required")
	}
	if !generatedSkillNamePattern.MatchString(name) {
		return skillservice.SaveRequest{}, QualityGateResult{}, fmt.Errorf("skill name %q must be lowercase kebab-case and match %s", name, generatedSkillNamePattern.String())
	}
	description := strings.TrimSpace(args.Description)
	if description == "" {
		return skillservice.SaveRequest{}, QualityGateResult{}, fmt.Errorf("description is required")
	}
	instructions := strings.TrimSpace(args.Instructions)
	if instructions == "" {
		return skillservice.SaveRequest{}, QualityGateResult{}, fmt.Errorf("instructions are required when raw_markdown is empty")
	}
	metadata := copyMetadata(args.Metadata)
	addGeneratedMetadata(metadata, args)
	qg := qualityGate(name, description, instructions)
	return skillservice.SaveRequest{
		Name:         name,
		Description:  description,
		Instructions: instructions + "\n",
		Version:      firstNonEmpty(args.Version, defaultVersion),
		Status:       safeDraftStatus(args.Status),
		Visibility:   safeVisibility(args.Visibility),
		Category:     firstNonEmpty(args.Category, defaultCategory),
		Owner:        firstNonEmpty(args.Owner, ctx.UserID()),
		Labels:       normalizeList(args.Labels),
		Tags:         generatedTags(args.Tags),
		AllowedTools: normalizeList(args.AllowedTools),
		Metadata:     metadata,
	}, qg, nil
}

func qualityGate(name, description, instructions string) QualityGateResult {
	qg := QualityGateResult{OK: true}
	addErr := func(msg string) { qg.OK = false; qg.Errors = append(qg.Errors, msg) }
	addWarn := func(msg string) { qg.Warnings = append(qg.Warnings, msg) }
	if !generatedSkillNamePattern.MatchString(name) {
		addErr("skill name must be stable lowercase kebab-case")
	}
	if len([]rune(description)) < 12 {
		addErr("description is too short")
	}
	body := strings.TrimSpace(instructions)
	if len([]rune(body)) < 800 {
		addErr("instructions are too short for a reusable skill; include concrete workflow, examples, failure modes, and validation criteria")
	}
	requiredSections := []string{"适用场景", "不适用场景", "核心原理", "执行步骤", "示例模板", "失败模式", "反过拟合", "验收标准"}
	for _, section := range requiredSections {
		if !strings.Contains(body, section) {
			addErr("missing required section containing: " + section)
		}
	}
	for _, term := range runtimeBodyForbiddenTerms() {
		if strings.Contains(description, term) || strings.Contains(body, term) {
			addErr("runtime skill body must be source-free; move source/project trace to metadata/evaluation only: " + term)
		}
	}
	for _, pattern := range runtimeBodyForbiddenPatterns() {
		if pattern.re.MatchString(description) || pattern.re.MatchString(body) {
			addErr("runtime skill body must be source-free; move source/project trace to metadata/evaluation only: " + pattern.label)
		}
	}
	if strings.Contains(body, "人物描写要丰满") || strings.Contains(body, "对白要自然") || strings.Contains(body, "节奏要紧凑") {
		addWarn("body contains generic writing slogans; replace them with executable steps")
	}
	if strings.Contains(body, "张三") || strings.Contains(body, "李四") {
		addWarn("body contains placeholder character names; make examples abstract or clearly illustrative")
	}
	return qg
}

type forbiddenPattern struct {
	label string
	re    *regexp.Regexp
}

func runtimeBodyForbiddenPatterns() []forbiddenPattern {
	return []forbiddenPattern{
		{label: "Chinese book-title quotes 《...》", re: regexp.MustCompile(`《[^》]+》`)},
		{label: "chapter reference 第N章", re: regexp.MustCompile(`第\s*([0-9]+|[一二三四五六七八九十百千〇零两]+)\s*章`)},
	}
}

func runtimeBodyForbiddenTerms() []string {
	return []string{
		"通过《",
		"基于《",
		"提炼自《",
		"从《",
		"来源书籍",
		"来源证据",
		"证据章节",
		"章节编号",
		"第1章",
		"第 1 章",
		"book_id",
		"project_id",
		"source_artifacts",
		"evidence_chapters",
		"chapter_skill_pack_",
		"cross_chapter_skill_candidates_",
		"reconstruction_gap_report_",
		"book_skill_batch_analysis__",
		"book_skill_delta__",
		"book_skill_eval__",
	}
}

func addGeneratedMetadata(metadata map[string]string, args SkillDraftArgs) {
	metadata["generated_by"] = "book_dissector"
	metadata["generated_at"] = time.Now().Format(time.RFC3339)
	if len(args.SourceArtifacts) > 0 {
		metadata["source_artifacts"] = strings.Join(normalizeList(args.SourceArtifacts), ",")
	}
	if len(args.EvidenceChapters) > 0 {
		metadata["evidence_chapters"] = strings.Join(normalizeList(args.EvidenceChapters), ",")
	}
}

func copyMetadata(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func generatedTags(tags []string) []string {
	base := []string{"novel", "writing", "generated", "skill-research"}
	base = append(base, tags...)
	return normalizeList(base)
}

func normalizeList(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func safeDraftStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "", "pending_review", "review":
		return defaultStatus
	case skillservice.StatusDraft:
		return skillservice.StatusDraft
	default:
		// Never publish directly from an LLM tool call. Human/admin action must publish.
		return defaultStatus
	}
}

func safeVisibility(visibility string) string {
	s := strings.ToLower(strings.TrimSpace(visibility))
	switch s {
	case skillservice.VisibilityPrivate, skillservice.VisibilityWorkspace, skillservice.VisibilityPublic:
		return s
	default:
		return defaultVisibility
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

func joinValidationErrors(v *skillservice.ValidationResult) string {
	if v == nil {
		return "unknown validation error"
	}
	parts := []string{}
	for _, item := range v.Errors {
		if item.Field != "" {
			parts = append(parts, item.Field+": "+item.Message)
		} else {
			parts = append(parts, item.Message)
		}
	}
	return strings.Join(parts, "; ")
}

func summaryContains(s skillservice.Summary, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		s.Name,
		s.Description,
		s.Category,
		strings.Join(s.Tags, " "),
		strings.Join(s.Labels, " "),
	}, " "))
	return strings.Contains(haystack, query)
}
