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

// Package artifactvalidation contains lightweight server-side guards for
// high-value model-generated artifacts. It is intentionally conservative: only
// artifacts with well-known protocol filenames are enforced. All other artifact
// names pass through unchanged.
package artifactvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SchemaChapterSkillPack        = "chapter_skill_pack"
	SchemaReconstructionGapReport = "reconstruction_gap_report"
	SchemaCrossChapterCandidates  = "cross_chapter_skill_candidates"
)

// Result describes the validation decision for an artifact save attempt.
type Result struct {
	FileName string   `json:"file_name"`
	SchemaID string   `json:"schema_id,omitempty"`
	Enforced bool     `json:"enforced"`
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
}

// Validate validates model-generated artifacts that match known protocol
// filenames. It does not enforce arbitrary JSON artifacts, because normal agent
// workspaces need to store many flexible files.
func Validate(fileName, mimeType string, data []byte) Result {
	res := Result{FileName: fileName, Valid: true}
	schemaID := SchemaForFileName(fileName)
	if schemaID == "" {
		return res
	}
	res.SchemaID = schemaID
	res.Enforced = true
	res.Valid = false

	if !isJSONLike(fileName, mimeType) {
		res.Errors = append(res.Errors, fmt.Sprintf("%s artifacts must be saved as JSON", schemaID))
		return res
	}

	var doc any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		res.Errors = append(res.Errors, "invalid JSON: "+err.Error())
		return res
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		res.Errors = append(res.Errors, "invalid JSON: multiple top-level values")
		return res
	}

	obj, ok := doc.(map[string]any)
	if !ok {
		res.Errors = append(res.Errors, "top-level value must be an object")
		return res
	}

	switch schemaID {
	case SchemaChapterSkillPack:
		res.Errors = validateChapterSkillPack(obj)
	case SchemaReconstructionGapReport:
		res.Errors = validateGapReport(obj)
	case SchemaCrossChapterCandidates:
		res.Errors = validateCrossChapterCandidates(obj)
	}
	res.Valid = len(res.Errors) == 0
	return res
}

// SchemaForFileName returns the enforced schema id for a known artifact name.
func SchemaForFileName(fileName string) string {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileName)), "user:")
	base := filepath.Base(name)
	switch {
	case strings.HasPrefix(base, "chapter_skill_pack_") && strings.HasSuffix(base, ".json"):
		return SchemaChapterSkillPack
	case strings.HasPrefix(base, "reconstruction_gap_report_") && strings.HasSuffix(base, ".json"):
		return SchemaReconstructionGapReport
	case strings.HasPrefix(base, "cross_chapter_skill_candidates_") && strings.HasSuffix(base, ".json"):
		return SchemaCrossChapterCandidates
	default:
		return ""
	}
}

func isJSONLike(fileName, mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	name := strings.ToLower(strings.TrimSpace(fileName))
	return strings.HasSuffix(name, ".json") || mimeType == "application/json" || strings.HasSuffix(mimeType, "+json")
}

func validateChapterSkillPack(o map[string]any) []string {
	v := validator{}
	v.allowedTop(o, []string{"book_id", "chapter_index", "chapter_title", "source_artifacts", "compressed_brief", "character_state", "scene_contract", "techniques", "style_fingerprint", "context_pack", "quality_bar"})
	v.reqString(o, "book_id")
	v.reqIntMin(o, "chapter_index", 1)
	v.reqStringAnyLen(o, "chapter_title")
	brief := v.reqString(o, "compressed_brief")
	if l := runeLen(brief); l > 0 && (l < 50 || l > 260) {
		v.err("compressed_brief must be 50-260 Chinese characters/runes, got %d", l)
	}

	scene := v.reqObject(o, "scene_contract")
	v.allowedTop(scene, []string{"opening_hook", "central_conflict", "turning_point", "ending_hook"})
	for _, k := range []string{"opening_hook", "central_conflict", "turning_point", "ending_hook"} {
		v.reqString(scene, "scene_contract."+k, k)
	}

	techs := v.reqArray(o, "techniques")
	if len(techs) < 3 {
		v.err("techniques must contain at least 3 items")
	}
	for i, item := range techs {
		tech, ok := item.(map[string]any)
		if !ok {
			v.err("techniques[%d] must be an object", i)
			continue
		}
		prefix := fmt.Sprintf("techniques[%d].", i)
		v.allowedTop(tech, []string{"name", "purpose", "execution_steps", "success_signals", "failure_modes", "transfer_scope", "anti_overfit_rule"}, prefix)
		for _, k := range []string{"name", "purpose"} {
			v.reqString(tech, prefix+k, k)
		}
		v.reqStringArrayMin(tech, prefix+"execution_steps", "execution_steps", 2)
		v.reqStringArrayMin(tech, prefix+"success_signals", "success_signals", 1)
		v.reqStringArrayMin(tech, prefix+"failure_modes", "failure_modes", 1)
	}

	style := v.reqObject(o, "style_fingerprint")
	v.allowedTop(style, []string{"pov", "sentence_rhythm", "dialogue_ratio", "sensory_texture", "tension_pattern"})
	for _, k := range []string{"pov", "sentence_rhythm", "dialogue_ratio", "sensory_texture", "tension_pattern"} {
		v.reqString(style, "style_fingerprint."+k, k)
	}

	if states, ok := o["character_state"]; ok {
		arr, ok := states.([]any)
		if !ok {
			v.err("character_state must be an array")
		} else {
			for i, item := range arr {
				state, ok := item.(map[string]any)
				if !ok {
					v.err("character_state[%d] must be an object", i)
					continue
				}
				prefix := fmt.Sprintf("character_state[%d].", i)
				v.allowedTop(state, []string{"name", "visible_goal", "hidden_pressure", "relationship_delta"}, prefix)
				for _, k := range []string{"name", "visible_goal", "hidden_pressure", "relationship_delta"} {
					v.reqString(state, prefix+k, k)
				}
			}
		}
	}
	return v.errors
}

func validateGapReport(o map[string]any) []string {
	v := validator{}
	v.allowedTop(o, []string{"book_id", "chapter_index", "attempt", "probe_artifact", "skill_pack_artifact", "overall_result", "gap_scores", "evidence", "decision", "skill_iteration_suggestions"})
	v.reqString(o, "book_id")
	v.reqIntMin(o, "chapter_index", 1)
	if _, ok := o["attempt"]; ok {
		v.reqIntMin(o, "attempt", 1)
	}
	v.reqEnum(o, "overall_result", []string{"pass", "partial", "fail"})
	v.reqEnum(o, "decision", []string{"accept_skill_pack", "refine_brief", "refine_skill_pack", "add_context_pack", "retry_probe", "request_human_review"})

	scores := v.reqObject(o, "gap_scores")
	v.allowedTop(scores, []string{"brief_gap", "skill_gap", "context_gap", "style_gap", "execution_gap"})
	for _, k := range []string{"brief_gap", "skill_gap", "context_gap", "style_gap", "execution_gap"} {
		v.reqIntRange(scores, "gap_scores."+k, k, 0, 5)
	}

	if evidence, ok := o["evidence"]; ok {
		arr, ok := evidence.([]any)
		if !ok {
			v.err("evidence must be an array")
		} else {
			for i, item := range arr {
				ev, ok := item.(map[string]any)
				if !ok {
					v.err("evidence[%d] must be an object", i)
					continue
				}
				prefix := fmt.Sprintf("evidence[%d].", i)
				v.allowedTop(ev, []string{"gap_type", "observation", "suggested_fix"}, prefix)
				v.reqEnum(ev, prefix+"gap_type", []string{"brief_gap", "skill_gap", "context_gap", "style_gap", "execution_gap"}, "gap_type")
				v.reqString(ev, prefix+"observation", "observation")
				v.reqString(ev, prefix+"suggested_fix", "suggested_fix")
			}
		}
	}
	return v.errors
}

func validateCrossChapterCandidates(o map[string]any) []string {
	v := validator{}
	v.allowedTop(o, []string{"book_id", "start_chapter", "end_chapter", "source_skill_packs", "stable_techniques", "candidate_techniques", "rejected_overfit_details", "upgrade_recommendations", "human_review_required"})
	v.reqString(o, "book_id")
	start := v.reqIntMin(o, "start_chapter", 1)
	end := v.reqIntMin(o, "end_chapter", 1)
	if start > 0 && end > 0 && end < start {
		v.err("end_chapter must be greater than or equal to start_chapter")
	}
	v.reqStringArrayMin(o, "source_skill_packs", "source_skill_packs", 2)
	v.reqTechniqueCandidates(o, "stable_techniques", true)
	if _, ok := o["candidate_techniques"]; ok {
		v.reqTechniqueCandidates(o, "candidate_techniques", false)
	}
	return v.errors
}

type validator struct{ errors []string }

func (v *validator) err(format string, args ...any) {
	v.errors = append(v.errors, fmt.Sprintf(format, args...))
}

func (v *validator) allowedTop(o map[string]any, allowed []string, prefixes ...string) {
	if o == nil {
		return
	}
	prefix := ""
	if len(prefixes) > 0 {
		prefix = prefixes[0]
	}
	set := map[string]bool{}
	for _, k := range allowed {
		set[k] = true
	}
	var extras []string
	for k := range o {
		if !set[k] {
			extras = append(extras, prefix+k)
		}
	}
	if len(extras) > 0 {
		sort.Strings(extras)
		v.err("unexpected fields: %s", strings.Join(extras, ", "))
	}
}

func (v *validator) reqString(o map[string]any, path string, keys ...string) string {
	key := path
	if len(keys) > 0 {
		key = keys[0]
	}
	s, ok := o[key].(string)
	if !ok || strings.TrimSpace(s) == "" {
		v.err("%s must be a non-empty string", path)
		return ""
	}
	return s
}

func (v *validator) reqStringAnyLen(o map[string]any, path string, keys ...string) string {
	key := path
	if len(keys) > 0 {
		key = keys[0]
	}
	s, ok := o[key].(string)
	if !ok {
		v.err("%s must be a string", path)
		return ""
	}
	return s
}

func (v *validator) reqEnum(o map[string]any, path string, allowed []string, keys ...string) string {
	s := v.reqString(o, path, keys...)
	if s == "" {
		return ""
	}
	for _, a := range allowed {
		if s == a {
			return s
		}
	}
	v.err("%s must be one of: %s", path, strings.Join(allowed, ", "))
	return s
}

func (v *validator) reqObject(o map[string]any, path string, keys ...string) map[string]any {
	key := path
	if len(keys) > 0 {
		key = keys[0]
	}
	child, ok := o[key].(map[string]any)
	if !ok || child == nil {
		v.err("%s must be an object", path)
		return nil
	}
	return child
}

func (v *validator) reqArray(o map[string]any, path string, keys ...string) []any {
	key := path
	if len(keys) > 0 {
		key = keys[0]
	}
	arr, ok := o[key].([]any)
	if !ok {
		v.err("%s must be an array", path)
		return nil
	}
	return arr
}

func (v *validator) reqStringArrayMin(o map[string]any, path, key string, min int) []string {
	arr := v.reqArray(o, path, key)
	if len(arr) < min {
		v.err("%s must contain at least %d items", path, min)
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok || strings.TrimSpace(s) == "" {
			v.err("%s[%d] must be a non-empty string", path, i)
			continue
		}
		out = append(out, s)
	}
	return out
}

func (v *validator) reqIntMin(o map[string]any, path string, min int, keys ...string) int {
	return v.reqIntRange(o, path, keyOrPath(path, keys...), min, int(^uint(0)>>1))
}

func (v *validator) reqIntRange(o map[string]any, path, key string, min, max int) int {
	val, ok := intFromJSON(o[key])
	if !ok {
		v.err("%s must be an integer", path)
		return 0
	}
	if val < min || val > max {
		v.err("%s must be between %d and %d", path, min, max)
	}
	return val
}

func (v *validator) reqTechniqueCandidates(o map[string]any, key string, requireMinTwoEvidence bool) {
	arr := v.reqArray(o, key)
	for i, item := range arr {
		tech, ok := item.(map[string]any)
		if !ok {
			v.err("%s[%d] must be an object", key, i)
			continue
		}
		prefix := fmt.Sprintf("%s[%d].", key, i)
		v.allowedTop(tech, []string{"name", "evidence_chapters", "general_pattern", "execution_steps", "applicability", "anti_overfit_rule", "validation_status"}, prefix)
		v.reqString(tech, prefix+"name", "name")
		minEvidence := 1
		if requireMinTwoEvidence {
			minEvidence = 2
		}
		v.reqStringArrayMin(tech, prefix+"evidence_chapters", "evidence_chapters", minEvidence)
		v.reqString(tech, prefix+"general_pattern", "general_pattern")
		v.reqStringArrayMin(tech, prefix+"execution_steps", "execution_steps", 2)
		v.reqString(tech, prefix+"applicability", "applicability")
		v.reqString(tech, prefix+"anti_overfit_rule", "anti_overfit_rule")
		v.reqEnum(tech, prefix+"validation_status", []string{"candidate", "ready_for_review", "needs_more_samples"}, "validation_status")
	}
}

func keyOrPath(path string, keys ...string) string {
	if len(keys) > 0 {
		return keys[0]
	}
	return path
}

func intFromJSON(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}

func runeLen(s string) int { return len([]rune(strings.TrimSpace(s))) }
