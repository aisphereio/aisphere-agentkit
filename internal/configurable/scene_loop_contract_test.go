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

package configurable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSceneSkillPackManagerDocumentsDeterministicLoopContract(t *testing.T) {
	manager := readAgentConfigForTest(t, "../../agents/book_scene_skill_pack_manager_mcp/root_agent.yaml")

	required := []string{
		"split_scene_cards -> agent_id=book_chapter_scene_splitter_mcp",
		"write_round1 -> agent_id=book_scene_imitation_writer_mcp",
		"evaluate_round1 -> agent_id=book_scene_gap_evaluator_mcp",
		"write_round2 -> agent_id=book_scene_imitation_writer_mcp",
		"evaluate_round2 -> agent_id=book_scene_gap_evaluator_mcp",
		"distill_final_pack -> agent_id=book_scene_skill_pack_distiller_mcp",
		"session_workspace_read_file",
		"If any required parent workspace path is missing or unreadable, do not invoke the distiller",
	}
	for _, want := range required {
		if !strings.Contains(manager.Instruction, want) {
			t.Fatalf("manager instruction missing loop contract text %q", want)
		}
	}
}

func TestSceneSkillPackManagerDistillerHandoffUsesInlineArtifacts(t *testing.T) {
	manager := readAgentConfigForTest(t, "../../agents/book_scene_skill_pack_manager_mcp/root_agent.yaml")

	required := []string{
		"book_scene_skill_pack_distiller_mcp runs in a fresh worker session and has no session_workspace_read_file tool",
		"Before dispatching distill_final_pack, the manager must call session_workspace_read_file",
		"The distill_final_pack task envelope must include inline artifact content, not only paths",
		"workspace_paths are refs for audit only",
	}
	for _, want := range required {
		if !strings.Contains(manager.Instruction, want) {
			t.Fatalf("manager instruction missing distiller handoff contract text %q", want)
		}
	}
}

func TestSceneSkillPackManagerRejectsBatchPathsForSceneArtifacts(t *testing.T) {
	manager := readAgentConfigForTest(t, "../../agents/book_scene_skill_pack_manager_mcp/root_agent.yaml")

	required := []string{
		"Every subagent task envelope must include output_paths",
		"For evaluate_round1, output_paths must include",
		"Do not accept, normalize, or continue with any batch_* path",
		"If a worker returns a ref/path outside output_paths, treat the task as failed",
	}
	for _, want := range required {
		if !strings.Contains(manager.Instruction, want) {
			t.Fatalf("manager instruction missing scene path contract text %q", want)
		}
	}
}

func TestSceneWorkersRequireOutputPathsAndForbidBatchNo(t *testing.T) {
	cases := []struct {
		path     string
		required []string
	}{
		{
			path: "../../agents/book_scene_imitation_writer_mcp/root_agent.yaml",
			required: []string{
				"output_paths as mandatory",
				"Never pass batch_no",
				"Do not return refs under batch_*",
			},
		},
		{
			path: "../../agents/book_scene_gap_evaluator_mcp/root_agent.yaml",
			required: []string{
				"output_paths as mandatory",
				"Never pass batch_no",
				"Do not return refs under batch_*",
			},
		},
	}
	for _, tc := range cases {
		cfg := readAgentConfigForTest(t, tc.path)
		for _, want := range tc.required {
			if !strings.Contains(cfg.Instruction, want) {
				t.Fatalf("%s instruction missing scene path contract text %q", tc.path, want)
			}
		}
		if !strings.Contains(readFileForTest(t, tc.path), "forbid_batch_no: true") {
			t.Fatalf("%s should set forbid_batch_no: true", tc.path)
		}
	}
}

func TestSceneSkillPackDistillerRequiresInlineInputs(t *testing.T) {
	distiller := readAgentConfigForTest(t, "../../agents/book_scene_skill_pack_distiller_mcp/root_agent.yaml")

	required := []string{
		"You run in a fresh worker session",
		"You do not have session_workspace_read_file",
		"Treat workspace_paths as audit refs only",
		"Required content must be provided inline by the manager in the task envelope",
		"If inline content is missing, return missing_input naming the missing inline fields, not the workspace paths",
	}
	for _, want := range required {
		if !strings.Contains(distiller.Instruction, want) {
			t.Fatalf("distiller instruction missing inline input contract text %q", want)
		}
	}
}

func TestSceneWorkersDoNotLoadBroadSkillPacks(t *testing.T) {
	paths := []string{
		"../../agents/book_scene_skill_pack_manager_mcp/root_agent.yaml",
		"../../agents/book_chapter_scene_splitter_mcp/root_agent.yaml",
		"../../agents/book_scene_imitation_writer_mcp/root_agent.yaml",
		"../../agents/book_scene_gap_evaluator_mcp/root_agent.yaml",
		"../../agents/book_scene_skill_pack_distiller_mcp/root_agent.yaml",
	}
	for _, path := range paths {
		cfg := readAgentConfigForTest(t, path)
		if len(cfg.Skills) != 0 {
			t.Fatalf("%s should keep a clean role-specific context, got skills %v", path, cfg.Skills)
		}
	}
}

func readAgentConfigForTest(t *testing.T, rel string) llmAgentYAMLConfig {
	t.Helper()
	path := filepath.Clean(rel)
	data := readFileForTest(t, path)
	var cfg llmAgentYAMLConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

func readFileForTest(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Clean(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
