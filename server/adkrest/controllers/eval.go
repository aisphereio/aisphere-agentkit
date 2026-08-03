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

import "net/http"

// ListEvalSetsHandler is a compatibility stub for the embedded ADK WebUI.
// The Go runtime does not yet implement full eval-set management, but the UI
// expects these endpoints to return JSON instead of HTTP 501.
func ListEvalSetsHandler(rw http.ResponseWriter, req *http.Request) {
	EncodeJSONResponse([]any{}, http.StatusOK, rw)
}

// SaveEvalSetHandler is a compatibility stub for the embedded ADK WebUI.
func SaveEvalSetHandler(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusOK)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true}, http.StatusOK, rw)
}

// ListEvalResultsHandler is a compatibility stub for the embedded ADK WebUI.
func ListEvalResultsHandler(rw http.ResponseWriter, req *http.Request) {
	EncodeJSONResponse([]any{}, http.StatusOK, rw)
}
