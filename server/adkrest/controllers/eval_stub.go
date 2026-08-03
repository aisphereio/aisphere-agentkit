// Copyright 2025 Google LLC
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

// ListEvalSetsStub is a local-first compatibility endpoint for the embedded
// ADK WebUI. The current Go runtime does not yet implement persistent eval
// sets, but the WebUI eagerly calls this endpoint when an app is selected.
// Returning an empty list keeps the normal Agent Builder/chat UI usable.
func ListEvalSetsStub(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	EncodeJSONResponse([]any{}, http.StatusOK, rw)
}

// SaveEvalSetStub acknowledges eval-set saves until the real eval subsystem is
// implemented. This prevents the embedded WebUI from treating eval support as a
// hard backend failure during builder workflows.
func SaveEvalSetStub(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true}, http.StatusOK, rw)
}

// ListEvalResultsStub is the matching compatibility endpoint for eval results.
func ListEvalResultsStub(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	EncodeJSONResponse([]any{}, http.StatusOK, rw)
}
