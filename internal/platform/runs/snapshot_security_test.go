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

package runs

import "testing"

func TestExecutionSnapshotRejectsCredentialNamingVariants(t *testing.T) {
	tests := []string{
		`{"connection":{"accessToken":"secret"}}`,
		`{"connection":{"access-token":"secret"}}`,
		`{"connection":{"clientSecret":"secret"}}`,
		`{"connection":{"refresh_token":"secret"}}`,
		`{"headers":{"Authorization":"Bearer secret"}}`,
		`{"ssh":{"privateKey":"secret"}}`,
	}
	for _, input := range tests {
		if _, err := CanonicalizeSnapshotJSON([]byte(input)); err == nil {
			t.Fatalf("CanonicalizeSnapshotJSON accepted credential value: %s", input)
		}
	}
}

func TestExecutionSnapshotAllowsCredentialReferencesAndSchemaProperties(t *testing.T) {
	input := `{
		"connection": {
			"credentialRef": "secret://github/user-1",
			"secretRef": "vault://tool-connections/github"
		},
		"inputSchema": {
			"type": "object",
			"properties": {
				"token": {"type": "string", "description": "One-time verification token"},
				"password": {"type": "string", "writeOnly": true}
			}
		}
	}`
	if _, err := CanonicalizeSnapshotJSON([]byte(input)); err != nil {
		t.Fatalf("CanonicalizeSnapshotJSON rejected references or schema properties: %v", err)
	}
}
