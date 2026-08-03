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

package artifactvalidation

import "testing"

func TestValidateChapterSkillPack(t *testing.T) {
	valid := []byte(`{
		"book_id":"book_a",
		"chapter_index":1,
		"chapter_title":"第一章",
		"source_artifacts":["chapter_analysis_book_a_1.md"],
		"compressed_brief":"主角在公开压力场中遭遇质疑，先用克制反应暴露关系张力，再延迟亮出关键证据完成反转，结尾留下更高层级压力，推动下一章继续验证主角能力。",
		"character_state":[{"name":"主角","visible_goal":"稳住局面","hidden_pressure":"不能暴露底牌","relationship_delta":"从被轻视到被重新评估"}],
		"scene_contract":{"opening_hook":"公开质疑","central_conflict":"能力真假","turning_point":"证据反转","ending_hook":"更高压力出现"},
		"techniques":[
			{"name":"公开压制延迟反转","purpose":"制造爽点","execution_steps":["先让压力落到主角身上","再让证据延迟出现"],"success_signals":["读者期待反击"],"failure_modes":["反转太早"],"transfer_scope":"强冲突章节","anti_overfit_rule":"不复刻原角色和道具"},
			{"name":"对白错位施压","purpose":"让人物关系变立体","execution_steps":["让对手用礼貌话施压","让主角用短句回避"],"success_signals":["潜台词清晰"],"failure_modes":["全员解释设定"],"transfer_scope":"对峙场","anti_overfit_rule":"只保留关系动作"},
			{"name":"结尾压力升级","purpose":"留下追读","execution_steps":["兑现当前冲突","引入更高层级问题"],"success_signals":["读者关心下一步"],"failure_modes":["结尾泄气"],"transfer_scope":"章节尾部","anti_overfit_rule":"换成任意更高层级压力"}
		],
		"style_fingerprint":{"pov":"第三人称贴近主角","sentence_rhythm":"短句推进","dialogue_ratio":"中高","sensory_texture":"少量动作细节","tension_pattern":"压制-蓄力-反转-加压"},
		"context_pack":{"required":false,"items":[]},
		"quality_bar":{"must_be_reusable":true,"must_be_executable":true,"must_avoid_plot_copy":true}
	}`)
	res := Validate("chapter_skill_pack_book_a_1.json", "application/json", valid)
	if !res.Enforced || !res.Valid || res.SchemaID != SchemaChapterSkillPack {
		t.Fatalf("expected valid enforced chapter skill pack, got %+v", res)
	}

	invalid := []byte(`{"book_id":"book_a","chapter_index":1,"compressed_brief":"太短","techniques":[]}`)
	res = Validate("chapter_skill_pack_book_a_1.json", "application/json", invalid)
	if !res.Enforced || res.Valid || len(res.Errors) == 0 {
		t.Fatalf("expected invalid enforced result, got %+v", res)
	}
}

func TestValidateGapReport(t *testing.T) {
	valid := []byte(`{
		"book_id":"book_a",
		"chapter_index":1,
		"attempt":1,
		"probe_artifact":"reconstruction_probe_book_a_1_1.md",
		"skill_pack_artifact":"chapter_skill_pack_book_a_1.json",
		"overall_result":"partial",
		"gap_scores":{"brief_gap":1,"skill_gap":3,"context_gap":0,"style_gap":2,"execution_gap":1},
		"evidence":[{"gap_type":"skill_gap","observation":"反转步骤缺少证据铺垫","suggested_fix":"补充证据前置和延迟揭示步骤"}],
		"decision":"refine_skill_pack",
		"skill_iteration_suggestions":[{"target":"techniques[0]","change":"增加证据铺垫规则","reason":"降低复写失败率"}]
	}`)
	res := Validate("reconstruction_gap_report_book_a_1_1.json", "application/json", valid)
	if !res.Enforced || !res.Valid || res.SchemaID != SchemaReconstructionGapReport {
		t.Fatalf("expected valid enforced gap report, got %+v", res)
	}

	invalid := []byte(`{"book_id":"book_a","chapter_index":1,"overall_result":"bad","gap_scores":{"brief_gap":8},"decision":"ship"}`)
	res = Validate("reconstruction_gap_report_book_a_1_1.json", "application/json", invalid)
	if !res.Enforced || res.Valid || len(res.Errors) == 0 {
		t.Fatalf("expected invalid enforced result, got %+v", res)
	}
}

func TestValidateCrossChapterCandidates(t *testing.T) {
	valid := []byte(`{
		"book_id":"book_a",
		"start_chapter":1,
		"end_chapter":3,
		"source_skill_packs":["chapter_skill_pack_book_a_1.json","chapter_skill_pack_book_a_2.json"],
		"stable_techniques":[{"name":"公开压制延迟反转","evidence_chapters":["1","2"],"general_pattern":"先压制再延迟反转","execution_steps":["建立压制","延迟揭示证据"],"applicability":"强冲突章节","anti_overfit_rule":"不保留原书角色道具","validation_status":"ready_for_review"}],
		"candidate_techniques":[{"name":"局部细节回钩","evidence_chapters":["3"],"general_pattern":"用小物件回钩情绪","execution_steps":["前置细节","后文回收"],"applicability":"情绪章节","anti_overfit_rule":"替换具体物件","validation_status":"needs_more_samples"}],
		"rejected_overfit_details":["原书专名"],
		"upgrade_recommendations":["进入人工审核"],
		"human_review_required":true
	}`)
	res := Validate("cross_chapter_skill_candidates_book_a_1_3.json", "application/json", valid)
	if !res.Enforced || !res.Valid || res.SchemaID != SchemaCrossChapterCandidates {
		t.Fatalf("expected valid cross-chapter candidates, got %+v", res)
	}
}

func TestValidateIgnoresOrdinaryArtifacts(t *testing.T) {
	res := Validate("notes.json", "application/json", []byte(`not-json`))
	if res.Enforced || !res.Valid {
		t.Fatalf("ordinary artifacts should not be enforced, got %+v", res)
	}
}
