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

func TestStripExecutionPlanAuthorizationRemovesEphemeralSkillDownloadURL(t *testing.T) {
	planJSON := []byte(`{
		"snapshotId": "abc123",
		"skills": [{
			"name": "rt-fn-1",
			"version": "v1.0.0",
			"object": "aihub:skill:rt-fn-1",
			"commitSHA": "commit-1",
			"treeSHA": "tree-1",
			"manifestSHA256": "manifest-1",
			"sha256": "package-1",
			"downloadUrl": "/v1/skills/rt-fn-1/packages?ref=v1.0.0&exp=1&sig=secret"
		}]
	}`)
	cleaned, err := stripExecutionPlanAuthorization(planJSON)
	if err != nil {
		t.Fatalf("stripExecutionPlanAuthorization: %v", err)
	}
	if string(cleaned) == string(planJSON) {
		t.Fatal("execution plan was not sanitized")
	}
	var object struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(cleaned, &object); err != nil {
		t.Fatalf("unmarshal cleaned plan: %v", err)
	}
	if len(object.Skills) != 1 {
		t.Fatalf("skills = %v", object.Skills)
	}
	if _, ok := object.Skills[0]["downloadUrl"]; ok {
		t.Fatal("ephemeral downloadUrl was archived")
	}
	if object.Skills[0]["object"] != "aihub:skill:rt-fn-1" || object.Skills[0]["version"] != "v1.0.0" ||
		object.Skills[0]["commitSHA"] != "commit-1" || object.Skills[0]["treeSHA"] != "tree-1" ||
		object.Skills[0]["manifestSHA256"] != "manifest-1" || object.Skills[0]["sha256"] != "package-1" {
		t.Fatalf("durable skill provenance was lost: %v", object.Skills[0])
	}
}
