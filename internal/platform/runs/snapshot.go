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

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Keys are normalized to lower-case alpha-numeric form so snake_case,
// kebab-case, and camelCase names are evaluated consistently.
var forbiddenSnapshotCredentialKeys = map[string]struct{}{
	"accesstoken":     {},
	"apikey":          {},
	"authorization":   {},
	"bearer":          {},
	"clientsecret":    {},
	"cookie":          {},
	"credentialvalue": {},
	"password":        {},
	"privatekey":      {},
	"refreshtoken":    {},
	"secret":          {},
	"setcookie":       {},
	"sshprivatekey":   {},
	"token":           {},
}

// CanonicalizeSnapshotJSON parses one JSON value, rejects credential values,
// and marshals it with deterministic map-key ordering. References such as
// credentialRef and secretRef are allowed; credential values are not.
func CanonicalizeSnapshotJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode execution snapshot: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, err
	}
	if err := validateSnapshotValue(value, "$"); err != nil {
		return nil, err
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical execution snapshot: %w", err)
	}
	return canonical, nil
}

func SnapshotDigest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("execution snapshot must contain exactly one JSON value")
	}
	return fmt.Errorf("decode trailing execution snapshot data: %w", err)
}

func validateSnapshotValue(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := normalizeSnapshotKey(key)
			if _, forbidden := forbiddenSnapshotCredentialKeys[normalized]; forbidden && containsCredentialValue(child) {
				return fmt.Errorf("execution snapshot contains forbidden credential field %s.%s", path, key)
			}
			if err := validateSnapshotValue(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := validateSnapshotValue(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeSnapshotKey(key string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(key))
}

func containsCredentialValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]any:
		// A Tool input/output schema may legitimately define a property named
		// token or password. Schema metadata is a contract, not a credential.
		return !looksLikeJSONSchemaNode(typed) && len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}

func looksLikeJSONSchemaNode(value map[string]any) bool {
	for _, key := range []string{"type", "$ref", "properties", "items", "oneOf", "anyOf", "allOf", "enum", "const"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}
