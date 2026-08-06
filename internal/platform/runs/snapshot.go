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
)

var forbiddenSnapshotKeys = map[string]struct{}{
	"access_token":     {},
	"api_key":          {},
	"apikey":           {},
	"client_secret":    {},
	"credential_value": {},
	"password":         {},
	"refresh_token":    {},
	"secret":           {},
	"token":            {},
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
			normalized := strings.ToLower(strings.TrimSpace(key))
			if _, forbidden := forbiddenSnapshotKeys[normalized]; forbidden {
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
