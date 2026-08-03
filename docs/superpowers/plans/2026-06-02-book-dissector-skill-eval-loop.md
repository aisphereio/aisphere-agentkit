# Book Dissector Skill Evaluation Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a repeatable loop where book_dissector extracts chapter skills, a context-isolated sub-agent reconstructs a sample from the brief plus extracted skills, and the platform records gaps for human-approved skill iteration.

**Architecture:** Keep the first working loop inside `book_dissector`, because chapter text, split manifest, chapter analysis, and writing-technique extraction are domain-specific. Expose the loop through platform-neutral artifacts and improvement proposals so the same pattern can later be reused by other agents. The sub-agent receives only a compressed brief, extracted `chapter_skill_pack`, and optional tiny `context_pack`; it must not read the original chapter.

**Tech Stack:** Go ADK runtime, YAML-configured LlmAgent/AgentTool, Angular chat UI, runtime trace/SSE logs, artifact service, platform improvement proposals, filesystem skills.

---

## File Structure

- Modify: `agentkit/agents/book_dissector/root_agent.yaml`
  - Owns domain orchestration: split book, read chapters, extract chapter analysis, create skill pack, call reconstruction probe, judge gaps.
- Modify: `agentkit/agents/book_dissector/chapter_reconstruction_probe_agent.yaml`
  - Owns isolated reconstruction from brief plus extracted chapter skill pack.
- Create: `agentkit/skills/novel-chapter-skill-pack/SKILL.md`
  - Defines the reusable artifact schema for chapter-level skill extraction.
- Create: `agentkit/skills/novel-chapter-reconstruction-eval/SKILL.md`
  - Defines reconstruction probe and gap-report rules.
- Create: `agentkit/agents/book_dissector/schemas/chapter_skill_pack.schema.json`
  - Machine-checkable schema for extracted chapter skills.
- Create: `agentkit/agents/book_dissector/schemas/reconstruction_gap_report.schema.json`
  - Machine-checkable schema for the probe evaluation result.
- Modify: `agentkit-web/src/app/components/event-content/event-content.component.ts`
  - Recognize reconstruction probe calls as agent handoff display.
- Modify: `agentkit-web/src/app/components/chat-panel/chat-panel.component.ts`
  - Summarize probe calls as agent handoffs, not regular tools.
- Modify: `agentkit/internal/llminternal/runtime_trace.go`
  - Respect `dump_llm_request` and `dump_llm_response` runtime config instead of only checking trace enabled.
- Modify: `agentkit/model/openai/openai.go`
  - Keep raw request/response capture behind explicit config and size limits.
- Modify: `agentkit/docs/agent-self-improvement-loop.md`
  - Add the book reconstruction loop as a concrete self-improvement pattern.
- Modify: `agentkit/docs/platform-improvement-proposals.md`
  - Add proposal types for `chapter_skill_pack` refinement and reconstruction gap review.
- Test: `agentkit/agents/book_dissector/*.yaml` via config loader tests.
- Test: `agentkit-web/src/app/components/chat-panel/chat-panel.component.spec.ts`.
- Test: Go runtime trace config tests under `agentkit/internal/runtimeconfig` or `agentkit/internal/llminternal`.

---

## Phase 0: Lock the Product Boundary

### Task 0.1: Define Ownership

**Files:**
- Modify: `agentkit/docs/agent-self-improvement-loop.md`

- [ ] **Step 1: Add ownership rule**

Add this section:

```markdown
## Book Skill Evaluation Loop Ownership

The first implementation of chapter reconstruction evaluation lives inside `book_dissector` because it depends on book split manifests, chapter text loading, chapter analysis, and genre-specific writing techniques.

The loop must emit platform-neutral artifacts:

- `chapter_analysis`
- `chapter_skill_pack`
- `reconstruction_probe`
- `reconstruction_gap_report`
- `skill_improvement_proposal`

Only the first two artifacts may be derived from original chapter text. The reconstruction probe receives only `compressed_brief`, `chapter_skill_pack`, and an optional small `context_pack`.

The platform owns durable capture, proposal review, approval, and skill versioning. `book_dissector` owns the domain-specific extraction and judging prompts.
```

- [ ] **Step 2: Verify wording**

Run:

```powershell
rg -n "Book Skill Evaluation Loop Ownership|chapter_skill_pack|reconstruction_gap_report" agentkit/docs/agent-self-improvement-loop.md
```

Expected: all three terms are present.

---

## Phase 1: Chapter Skill Pack Artifact

### Task 1.1: Create the Chapter Skill Pack Skill

**Files:**
- Create: `agentkit/skills/novel-chapter-skill-pack/SKILL.md`

- [ ] **Step 1: Write the skill**

Create:

```markdown
---
name: novel-chapter-skill-pack
description: Extract reusable chapter-level writing techniques from a dissected novel chapter into a bounded skill pack artifact.
---

# Novel Chapter Skill Pack

Use this skill when `book_dissector` has already loaded one chapter and needs to extract reusable writing techniques that a separate reconstruction agent can use without seeing the original chapter.

## Input Boundary

Allowed input:

- Chapter title and index.
- A bounded chapter excerpt or chapter analysis produced by `book_dissector`.
- Optional previous-tail and next-head summaries.

Do not copy large original passages into the output. The skill pack is not a summary of the plot; it is a reusable method pack.

## Output Artifact

Save as:

`chapter_skill_pack_<book_id>_<chapter_index>.json`

The artifact must match:

```json
{
  "book_id": "string",
  "chapter_index": 1,
  "chapter_title": "string",
  "source_artifacts": ["chapter_analysis_..."],
  "compressed_brief": "100-200 Chinese characters",
  "character_state": [
    {
      "name": "string",
      "visible_goal": "string",
      "hidden_pressure": "string",
      "relationship_delta": "string"
    }
  ],
  "scene_contract": {
    "opening_hook": "string",
    "central_conflict": "string",
    "turning_point": "string",
    "ending_hook": "string"
  },
  "techniques": [
    {
      "name": "string",
      "purpose": "string",
      "execution_steps": ["string"],
      "success_signals": ["string"],
      "failure_modes": ["string"]
    }
  ],
  "style_fingerprint": {
    "pov": "string",
    "sentence_rhythm": "string",
    "dialogue_ratio": "string",
    "sensory_texture": "string",
    "tension_pattern": "string"
  },
  "context_pack": {
    "required": false,
    "items": ["string"]
  }
}
```

## Quality Bar

Each technique must be executable. Avoid labels such as "make it exciting" unless paired with concrete steps, timing, and observable effects.
```

- [ ] **Step 2: Verify skill exists**

Run:

```powershell
Test-Path agentkit/skills/novel-chapter-skill-pack/SKILL.md
```

Expected: `True`.

### Task 1.2: Add a JSON Schema

**Files:**
- Create: `agentkit/agents/book_dissector/schemas/chapter_skill_pack.schema.json`

- [ ] **Step 1: Create schema**

Create:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "ChapterSkillPack",
  "type": "object",
  "required": [
    "book_id",
    "chapter_index",
    "chapter_title",
    "compressed_brief",
    "scene_contract",
    "techniques",
    "style_fingerprint"
  ],
  "properties": {
    "book_id": {"type": "string", "minLength": 1},
    "chapter_index": {"type": "integer", "minimum": 1},
    "chapter_title": {"type": "string"},
    "source_artifacts": {"type": "array", "items": {"type": "string"}},
    "compressed_brief": {"type": "string", "minLength": 50, "maxLength": 260},
    "character_state": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["name", "visible_goal", "hidden_pressure", "relationship_delta"],
        "properties": {
          "name": {"type": "string"},
          "visible_goal": {"type": "string"},
          "hidden_pressure": {"type": "string"},
          "relationship_delta": {"type": "string"}
        }
      }
    },
    "scene_contract": {
      "type": "object",
      "required": ["opening_hook", "central_conflict", "turning_point", "ending_hook"],
      "properties": {
        "opening_hook": {"type": "string"},
        "central_conflict": {"type": "string"},
        "turning_point": {"type": "string"},
        "ending_hook": {"type": "string"}
      }
    },
    "techniques": {
      "type": "array",
      "minItems": 3,
      "items": {
        "type": "object",
        "required": ["name", "purpose", "execution_steps", "success_signals", "failure_modes"],
        "properties": {
          "name": {"type": "string"},
          "purpose": {"type": "string"},
          "execution_steps": {"type": "array", "minItems": 2, "items": {"type": "string"}},
          "success_signals": {"type": "array", "minItems": 1, "items": {"type": "string"}},
          "failure_modes": {"type": "array", "minItems": 1, "items": {"type": "string"}}
        }
      }
    },
    "style_fingerprint": {
      "type": "object",
      "required": ["pov", "sentence_rhythm", "dialogue_ratio", "sensory_texture", "tension_pattern"],
      "properties": {
        "pov": {"type": "string"},
        "sentence_rhythm": {"type": "string"},
        "dialogue_ratio": {"type": "string"},
        "sensory_texture": {"type": "string"},
        "tension_pattern": {"type": "string"}
      }
    },
    "context_pack": {
      "type": "object",
      "properties": {
        "required": {"type": "boolean"},
        "items": {"type": "array", "items": {"type": "string"}}
      }
    }
  }
}
```

- [ ] **Step 2: Validate JSON syntax**

Run:

```powershell
node -e "JSON.parse(require('fs').readFileSync('agentkit/agents/book_dissector/schemas/chapter_skill_pack.schema.json','utf8')); console.log('ok')"
```

Expected: `ok`.

---

## Phase 2: Reconstruction Probe and Gap Report

### Task 2.1: Upgrade the Probe Agent Input Contract

**Files:**
- Modify: `agentkit/agents/book_dissector/chapter_reconstruction_probe_agent.yaml`

- [ ] **Step 1: Update instruction**

Change the input boundary so it explicitly allows:

```yaml
  输入边界：
  - 你只能使用当前请求里的 compressed_brief、chapter_skill_pack、style_fingerprint、target_techniques，以及可选 tiny context_pack。
  - 你不能请求或读取原章全文、manifest、chapter_analysis artifact、长上下文或未压缩原始内容。
  - 你必须先根据 chapter_skill_pack 写测试样稿，再诊断哪些问题来自 brief 缺口、skill 缺口、context 缺口、style 缺口或执行缺口。
```

- [ ] **Step 2: Update output format**

Require these sections:

```markdown
## 测试样稿

## 结果判断

## 缺口分类

## skill_pack 改进建议

## brief/context_pack 改进建议
```

- [ ] **Step 3: Run config tests**

Run:

```powershell
cd agentkit
go test ./internal/configurable ./tool/agenttool
```

Expected: PASS.

### Task 2.2: Create Gap Report Schema

**Files:**
- Create: `agentkit/agents/book_dissector/schemas/reconstruction_gap_report.schema.json`

- [ ] **Step 1: Create schema**

Create:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "ReconstructionGapReport",
  "type": "object",
  "required": ["book_id", "chapter_index", "overall_result", "gap_scores", "decision"],
  "properties": {
    "book_id": {"type": "string"},
    "chapter_index": {"type": "integer", "minimum": 1},
    "probe_artifact": {"type": "string"},
    "skill_pack_artifact": {"type": "string"},
    "overall_result": {
      "type": "string",
      "enum": ["pass", "partial", "fail"]
    },
    "gap_scores": {
      "type": "object",
      "required": ["brief_gap", "skill_gap", "context_gap", "style_gap", "execution_gap"],
      "properties": {
        "brief_gap": {"type": "integer", "minimum": 0, "maximum": 5},
        "skill_gap": {"type": "integer", "minimum": 0, "maximum": 5},
        "context_gap": {"type": "integer", "minimum": 0, "maximum": 5},
        "style_gap": {"type": "integer", "minimum": 0, "maximum": 5},
        "execution_gap": {"type": "integer", "minimum": 0, "maximum": 5}
      }
    },
    "evidence": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["gap_type", "observation", "suggested_fix"],
        "properties": {
          "gap_type": {
            "type": "string",
            "enum": ["brief_gap", "skill_gap", "context_gap", "style_gap", "execution_gap"]
          },
          "observation": {"type": "string"},
          "suggested_fix": {"type": "string"}
        }
      }
    },
    "decision": {
      "type": "string",
      "enum": ["accept_skill_pack", "refine_brief", "refine_skill_pack", "add_context_pack", "retry_probe", "request_human_review"]
    }
  }
}
```

- [ ] **Step 2: Validate JSON syntax**

Run:

```powershell
node -e "JSON.parse(require('fs').readFileSync('agentkit/agents/book_dissector/schemas/reconstruction_gap_report.schema.json','utf8')); console.log('ok')"
```

Expected: `ok`.

---

## Phase 3: Book Dissector Orchestration

### Task 3.1: Bind New Skills to book_dissector

**Files:**
- Modify: `agentkit/agents/book_dissector/root_agent.yaml`

- [ ] **Step 1: Add skills**

Add:

```yaml
skills:
  - novel-book-dissect-core
  - novel-chapter-function-analysis
  - novel-writing-skill-extraction
  - novel-chapter-skill-pack
  - novel-chapter-reconstruction-eval
```

- [ ] **Step 2: Update blind-probe workflow**

Add this workflow rule:

```yaml
  - 用户要求“压缩第一章测试 skill / 让子 agent 复原 / 看 skill 是否足够”时，必须执行：
    1. 用 book_get_chapter 获取目标章节和必要邻接上下文。
    2. 保存 chapter_analysis artifact。
    3. 保存 chapter_skill_pack artifact，包含 compressed_brief、scene_contract、techniques、style_fingerprint、optional context_pack。
    4. 调用 book_chapter_reconstruction_probe，只传 compressed_brief、chapter_skill_pack、style_fingerprint、optional context_pack。
    5. 读取子 agent 样稿后，和原章分析进行对比，保存 reconstruction_gap_report。
    6. 给出决策：accept_skill_pack / refine_brief / refine_skill_pack / add_context_pack / retry_probe / request_human_review。
```

- [ ] **Step 3: Run config tests**

Run:

```powershell
cd agentkit
go test ./internal/configurable ./tool/agenttool ./tool/skilltoolset/skill
```

Expected: PASS.

### Task 3.2: Define Artifact Naming

**Files:**
- Modify: `agentkit/agents/book_dissector/root_agent.yaml`

- [ ] **Step 1: Add naming rules**

Add:

```yaml
  产物命名：
  - chapter_analysis_<book_id>_<chapter_index>.md
  - chapter_skill_pack_<book_id>_<chapter_index>.json
  - reconstruction_probe_<book_id>_<chapter_index>_<attempt>.md
  - reconstruction_gap_report_<book_id>_<chapter_index>_<attempt>.json
```

- [ ] **Step 2: Verify references**

Run:

```powershell
rg -n "chapter_skill_pack_|reconstruction_probe_|reconstruction_gap_report_" agentkit/agents/book_dissector/root_agent.yaml
```

Expected: all names are present.

---

## Phase 4: UI and User Visibility

### Task 4.1: Keep Handoff UI Working for Reconstruction Probe

**Files:**
- Modify: `agentkit-web/src/app/components/event-content/event-content.component.ts`
- Modify: `agentkit-web/src/app/components/chat-panel/chat-panel.component.ts`
- Test: `agentkit-web/src/app/components/chat-panel/chat-panel.component.spec.ts`

- [ ] **Step 1: Confirm probe handoff recognition**

Run:

```powershell
rg -n "book_chapter_reconstruction_probe|agentToolNames|handoffTarget" agentkit-web/src/app/components/event-content agentkit-web/src/app/components/chat-panel
```

Expected: the probe is recognized as a handoff target.

- [ ] **Step 2: Add future-proof registry follow-up**

Add this comment near the `agentToolNames` set:

```ts
// TODO: Replace this local allowlist with runtime AgentTool metadata once
// backend exposes configured child-agent tool names in app metadata.
```

- [ ] **Step 3: Build frontend**

Run:

```powershell
cd agentkit-web
npm run build
```

Expected: build succeeds. Existing Angular warnings may remain.

### Task 4.2: Add Skill Evaluation Result Panel Hook

**Files:**
- Modify: `agentkit-web/src/app/components/artifact-tab/artifact-tab.component.ts`
- Modify: `agentkit-web/src/app/components/artifact-tab/artifact-tab.component.html`

- [ ] **Step 1: Classify reconstruction artifacts**

Add a helper:

```ts
isSkillEvalArtifact(name: string): boolean {
  return name.startsWith('chapter_skill_pack_') ||
      name.startsWith('reconstruction_probe_') ||
      name.startsWith('reconstruction_gap_report_');
}
```

- [ ] **Step 2: Display an artifact badge**

When `isSkillEvalArtifact(artifact.name)` is true, show a compact badge named `Skill Eval`.

- [ ] **Step 3: Preserve lazy loading**

Do not call artifact content loading during list rendering. Only fetch content on explicit open/preview.

- [ ] **Step 4: Build frontend**

Run:

```powershell
cd agentkit-web
npm run build
```

Expected: build succeeds.

---

## Phase 5: Raw Request/Response Capture Controls

### Task 5.1: Respect Trace Dump Flags

**Files:**
- Modify: `agentkit/internal/llminternal/runtime_trace.go`
- Modify: `agentkit/internal/runtimeconfig/config.go`
- Test: `agentkit/internal/runtimeconfig/config_test.go`

- [ ] **Step 1: Add tests**

Add a test that config with:

```yaml
runtime:
  trace:
    enabled: true
    dump_llm_request: false
    dump_llm_response: false
```

does not emit full `llm.request.final` contents or `llm.response.final` contents.

- [ ] **Step 2: Implement flag checks**

In runtime trace recording, check both trace enabled and the dump flags before recording final LLM request/response payload contents.

- [ ] **Step 3: Preserve summary events**

Even when dumps are disabled, keep bounded metadata:

```json
{
  "event_type": "llm.request.final",
  "model": "...",
  "content_count": 3,
  "tool_count": 2,
  "content_omitted": true
}
```

- [ ] **Step 4: Run Go tests**

Run:

```powershell
cd agentkit
go test ./internal/runtimeconfig ./internal/llminternal ./internal/runtimetrace
```

Expected: PASS.

### Task 5.2: Mark Raw HTTP Dumps as High Sensitivity

**Files:**
- Modify: `agentkit/model/openai/openai.go`
- Modify: `agentkit/docs/runtime-log-sse.md`

- [ ] **Step 1: Document policy**

Add:

```markdown
`openai.request.payload`, `openai.response.raw`, and `openai.stream.raw` are high-sensitivity diagnostic events. They should be disabled by default, size-limited, and excluded from background skill evolution unless the user explicitly enables raw capture for a run.
```

- [ ] **Step 2: Verify config path**

Run:

```powershell
rg -n "openai.request.payload|openai.response.raw|dump_llm_request|dump_llm_response" agentkit
```

Expected: docs and code reference these event names.

---

## Phase 6: Platform Improvement Proposal Integration

### Task 6.1: Add Proposal Types

**Files:**
- Modify: `agentkit/docs/platform-improvement-proposals.md`

- [ ] **Step 1: Add issue types**

Add:

```markdown
Skill evaluation loop issue types:

- `chapter_skill_gap`
- `chapter_brief_gap`
- `chapter_context_gap`
- `chapter_style_gap`
- `reconstruction_execution_gap`
```

- [ ] **Step 2: Add proposal types**

Add:

```markdown
Skill evaluation loop proposal types:

- `update_chapter_skill_pack`
- `promote_chapter_skill_pack_to_skill`
- `update_reconstruction_brief_schema`
- `add_minimal_context_pack`
- `retry_reconstruction_probe`
```

### Task 6.2: Human Decision Gate

**Files:**
- Modify: `agentkit/docs/platform-improvement-proposals.md`
- Modify: `agentkit/docs/agent-self-improvement-loop.md`

- [ ] **Step 1: Add approval rule**

Add:

```markdown
The platform must not automatically publish a new skill from reconstruction results. It may draft an improvement proposal, but promotion to a reusable skill requires explicit human approval.
```

- [ ] **Step 2: Verify**

Run:

```powershell
rg -n "must not automatically publish|explicit human approval|promote_chapter_skill_pack_to_skill" agentkit/docs/platform-improvement-proposals.md agentkit/docs/agent-self-improvement-loop.md
```

Expected: approval rule is present.

---

## Phase 7: End-to-End Scenario

### Task 7.1: Manual E2E Test Script

**Files:**
- Create: `agentkit/docs/book-dissector-reconstruction-e2e.md`

- [ ] **Step 1: Add test scenario**

Create:

```markdown
# Book Dissector Reconstruction E2E

## Preconditions

- Backend is running.
- Frontend is running.
- `book_dissector` app is selected.
- A TXT/Markdown novel upload exists in platform uploads.

## Scenario

1. Send an upload reference to `book_dissector`.
2. Confirm the agent uses `upload_get`, `upload_attach_artifact`, and `book_split_from_artifact`.
3. Confirm the agent reports `book_id`, chapter count, first 20 chapter titles, and warnings.
4. Ask: `用第一章提炼出的 skill pack 做一次盲测复原，只给子 agent brief 和 skill_pack，不给原文。`
5. Confirm artifacts are created:
   - `chapter_analysis_<book_id>_1.md`
   - `chapter_skill_pack_<book_id>_1.json`
   - `reconstruction_probe_<book_id>_1_1.md`
   - `reconstruction_gap_report_<book_id>_1_1.json`
6. Confirm UI displays `book_dissector -> book_chapter_reconstruction_probe`.
7. Confirm artifact list shows names first and only loads content after explicit open/preview.
8. Confirm final answer includes the decision:
   - `accept_skill_pack`
   - `refine_brief`
   - `refine_skill_pack`
   - `add_context_pack`
   - `retry_probe`
   - `request_human_review`
```

- [ ] **Step 2: Run manual scenario**

Run through the browser at:

```text
http://localhost:4200/?app=book_dissector&userId=user&session=<session>
```

Expected: all listed artifacts and UI handoff appear.

---

## Phase 8: Backlog for Platform Generalization

### Task 8.1: Replace Local AgentTool Allowlist

**Files:**
- Modify: `agentkit/server/adkrest/controllers/metadata.go`
- Modify: `agentkit-web/src/app/core/services/agent.service.ts`
- Modify: `agentkit-web/src/app/components/chat-panel/chat-panel.component.ts`
- Modify: `agentkit-web/src/app/components/event-content/event-content.component.ts`

- [ ] **Step 1: Backend exposes child agent tools**

Expose configured AgentTool names in app metadata:

```json
{
  "agent_tools": [
    {
      "tool_name": "book_chapter_reconstruction_probe",
      "agent_name": "book_chapter_reconstruction_probe",
      "parent_agent": "book_dissector"
    }
  ]
}
```

- [ ] **Step 2: Frontend consumes metadata**

Replace the local `agentToolNames` allowlist with metadata-driven names.

- [ ] **Step 3: Test with book_dissector**

Expected: handoff UI still works after removing the hardcoded probe name.

### Task 8.2: Add Generic Skill Eval Loop Agent

**Files:**
- Create: `agentkit/agents/skill_eval_loop/root_agent.yaml`
- Create: `agentkit/skills/skill-eval-loop-core/SKILL.md`

- [ ] **Step 1: Create platform-neutral evaluator**

The generic agent receives:

```json
{
  "source_agent": "book_dissector",
  "domain": "novel_chapter",
  "input_artifacts": ["chapter_skill_pack_...", "reconstruction_probe_..."],
  "judge_policy": "compare_against_source_analysis"
}
```

- [ ] **Step 2: Keep book-specific extraction outside generic agent**

The generic evaluator must not load original chapter text. It only judges artifacts and drafts improvement proposals.

---

## Self-Review

- Spec coverage:
  - Artifact lazy loading: covered in Task 4.2.
  - Handoff UI: covered in Task 4.1.
  - Raw request/response capture: covered in Phase 5.
  - SkillClaw-style proxy/evolver idea: covered by Phase 6 and Phase 8.
  - Book dissection reconstruction loop: covered by Phases 1-3 and Phase 7.
  - Human decision gate: covered in Task 6.2.

- Placeholder scan:
  - No `TBD` placeholders.
  - Backlog tasks are intentionally scoped with concrete expected payloads.

- Type consistency:
  - `chapter_skill_pack`, `reconstruction_probe`, and `reconstruction_gap_report` names are consistent across tasks.
  - Gap categories are consistent: `brief_gap`, `skill_gap`, `context_gap`, `style_gap`, `execution_gap`.

---

## Execution Options

1. **Subagent-Driven (recommended)**: Dispatch a fresh worker per phase and review after each phase.
2. **Inline Execution**: Execute phases in this session with checkpoints after each phase.

Recommended first execution slice:

1. Phase 1: create `novel-chapter-skill-pack` and schema.
2. Phase 2: upgrade reconstruction probe and gap schema.
3. Phase 3: update `book_dissector` orchestration.
4. Phase 7: run one manual E2E scenario.
