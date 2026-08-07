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

import "gorm.io/gorm"

// AfterCreate runs inside the CreateAttempt transaction. When a failed or
// cancelled Run is retried, terminal timestamps and failure details from the
// previous Attempt must not remain attached to the newly queued Run.
func (a *RunAttempt) AfterCreate(tx *gorm.DB) error {
	if a == nil || a.TenantID == "" || a.RunID == "" {
		return nil
	}
	return tx.Model(&Run{}).
		Where("tenant_id = ? AND id = ?", a.TenantID, a.RunID).
		Updates(map[string]any{
			"failure_code":  "",
			"error_message": "",
			"finished_at":   nil,
			"cancelled_at":  nil,
		}).Error
}
