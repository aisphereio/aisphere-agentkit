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

package aihubruntime

import "errors"

const (
	CodeSkillPackageURLRequired    = "SKILL_PACKAGE_URL_REQUIRED"
	CodeSkillPackageDigestMismatch = "SKILL_PACKAGE_DIGEST_MISMATCH"
	CodeSkillPackageDownloadFailed = "SKILL_PACKAGE_DOWNLOAD_FAILED"
	CodeSkillPackageUnpackFailed   = "SKILL_PACKAGE_UNPACK_FAILED"
	CodeSkillMaterializeFailed     = "SKILL_MATERIALIZE_FAILED"
)

type SkillRuntimeError struct {
	Code string
	Err  error
}

func (e *SkillRuntimeError) Error() string {
	if e == nil || e.Err == nil {
		return "skill runtime failed"
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *SkillRuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func skillRuntimeError(code string, err error) error {
	if err == nil {
		err = errors.New("skill runtime failed")
	}
	return &SkillRuntimeError{Code: code, Err: err}
}

// NewSkillMaterializeError lets the session/sandbox integration preserve one
// stable failure category without exposing the rest of this package's error
// construction details.
func NewSkillMaterializeError(err error) error {
	return skillRuntimeError(CodeSkillMaterializeFailed, err)
}

func SkillFailureCode(err error) string {
	var target *SkillRuntimeError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}
