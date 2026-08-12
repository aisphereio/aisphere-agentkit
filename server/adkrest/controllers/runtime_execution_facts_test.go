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
	"encoding/json"
	"testing"
)

// The ledger credential scanner rejects any field named authorization (the
// value of an HTTP Authorization header is a credential). Hub plan snapshots
// carry an authorization subtree that is execution context, not source — the
// archived copy must strip it while keeping the rest of the plan intact.
func TestStripExecutionPlanAuthorization(t *testing.T) {
	planJSON := []byte(`{
		"snapshotId": "abc123",
		"agent": {"id": "close-ag-1", "version": "1.0.0"},
		"authorization": {
			"principalSubject": "user:496333c7-7acc-4717-8596-056544fc0a68",
			"tools": [{"tool": "demo.fn.run", "approved": true}]
		},
		"skills": [{"name": "rt-fn-1", "version": "1.0.0"}]
	}`)
	cleaned, err := stripExecutionPlanAuthorization(planJSON)
	if err != nil {
		t.Fatalf("stripExecutionPlanAuthorization: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &object); err != nil {
		t.Fatalf("unmarshal cleaned plan: %v", err)
	}
	if _, ok := object["authorization"]; ok {
		t.Fatal("authorization subtree was not stripped")
	}
	if len(object["agent"]) == 0 || len(object["skills"]) == 0 || len(object["snapshotId"]) == 0 {
		t.Fatalf("plan lost non-authorization fields: %s", cleaned)
	}
}