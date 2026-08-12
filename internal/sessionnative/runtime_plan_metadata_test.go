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

package sessionnative

import (
	"testing"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/sandboxclient"
)

func TestSessionLeaseStateDeltaIncludesRuntimePlan(t *testing.T) {
	lease := &SessionLease{
		SessionID: "session-1", AgentID: "agent-1", SnapshotID: "snap-1",
		Sandbox: &sandboxclient.Lease{SandboxID: "sandbox-1"},
		Plan: &runtimeplan.RuntimePlan{
			SnapshotID: "snap-1",
			Agent:      runtimeplan.AgentSpec{ID: "agent-1", Name: "research_agent"},
			Tools:      []runtimeplan.ToolBinding{{Name: "workspace.read", RuntimeType: "sandbox"}},
		},
	}
	delta := lease.StateDelta()
	native, ok := delta["__agent_native_sandbox__"].(map[string]any)
	if !ok {
		t.Fatalf("missing native sandbox state: %+v", delta)
	}
	plan, ok := native["runtimePlan"].(map[string]any)
	if !ok {
		t.Fatalf("missing runtime plan: %+v", native)
	}
	if plan["snapshotId"] != "snap-1" {
		t.Fatalf("snapshotId = %v, want snap-1", plan["snapshotId"])
	}
}
