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
	"fmt"
	"net/http"

	"google.golang.org/adk/server/adkrest/internal/models"
)

// validateRunInputPolicy protects the model runtime from accidental direct file
// injection. Frontends should upload files through Platform Uploads, then send a
// small upload_id/reference message to the agent.
func (c *RuntimeAPIController) validateRunInputPolicy(runReq models.RunAgentRequest) error {
	if c == nil || c.runtimeConfig == nil || !c.runtimeConfig.Runtime.InputPolicy.RejectLargeInline {
		return nil
	}
	policy := c.runtimeConfig.Runtime.InputPolicy
	for i, part := range runReq.NewMessage.Parts {
		if part == nil {
			continue
		}
		if policy.MaxInlineTextChars > 0 && len([]rune(part.Text)) > policy.MaxInlineTextChars {
			return newStatusError(fmt.Errorf("message part %d contains %d text chars, exceeding runtime.input_policy.max_inline_text_chars=%d; upload large files through /platform/uploads and send only upload_id/artifact references to the agent", i, len([]rune(part.Text)), policy.MaxInlineTextChars), http.StatusRequestEntityTooLarge)
		}
		if part.InlineData != nil && policy.MaxInlineDataBytes > 0 && int64(len(part.InlineData.Data)) > policy.MaxInlineDataBytes {
			return newStatusError(fmt.Errorf("message part %d contains %d inline data bytes, exceeding runtime.input_policy.max_inline_data_bytes=%d; upload files through /platform/uploads instead of embedding them in newMessage.parts", i, len(part.InlineData.Data), policy.MaxInlineDataBytes), http.StatusRequestEntityTooLarge)
		}
	}
	return nil
}
