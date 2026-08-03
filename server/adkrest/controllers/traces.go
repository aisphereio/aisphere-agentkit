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
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"google.golang.org/adk/internal/runtimetrace"
)

// RuntimeTraceAPIController exposes platform runtime traces for debugging.
type RuntimeTraceAPIController struct {
	recorder runtimetrace.Recorder
}

func NewRuntimeTraceAPIController(recorder runtimetrace.Recorder) *RuntimeTraceAPIController {
	return &RuntimeTraceAPIController{recorder: recorder}
}

func (c *RuntimeTraceAPIController) ListTracesHandler(rw http.ResponseWriter, req *http.Request) {
	if c == nil || c.recorder == nil || !c.recorder.Enabled() {
		EncodeJSONResponse(map[string]any{"enabled": false, "traces": []any{}}, http.StatusOK, rw)
		return
	}
	traces, err := c.recorder.List()
	if err != nil {
		EncodeJSONResponse(map[string]any{"error": err.Error()}, http.StatusInternalServerError, rw)
		return
	}
	EncodeJSONResponse(map[string]any{"enabled": true, "root": c.recorder.Root(), "traces": traces}, http.StatusOK, rw)
}

func (c *RuntimeTraceAPIController) GetTraceHandler(rw http.ResponseWriter, req *http.Request) {
	if c == nil || c.recorder == nil || !c.recorder.Enabled() {
		EncodeJSONResponse(map[string]any{"enabled": false, "events": []any{}}, http.StatusOK, rw)
		return
	}
	invocationID := mux.Vars(req)["invocation_id"]
	if invocationID == "" {
		EncodeJSONResponse(map[string]any{"error": "missing invocation_id"}, http.StatusBadRequest, rw)
		return
	}
	if strings.HasPrefix(invocationID, "user_") {
		EncodeJSONResponse(map[string]any{
			"error":         "invalid runtime trace invocation_id",
			"invocation_id": invocationID,
			"reason":        "user event ids are not runtime trace invocation ids",
		}, http.StatusBadRequest, rw)
		return
	}
	limit := 1000
	if raw := req.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	events, err := c.recorder.Read(invocationID, limit)
	if err != nil {
		if os.IsNotExist(err) {
			EncodeJSONResponse(map[string]any{"error": "runtime trace not found", "invocation_id": invocationID}, http.StatusNotFound, rw)
			return
		}
		EncodeJSONResponse(map[string]any{"error": err.Error()}, http.StatusInternalServerError, rw)
		return
	}
	EncodeJSONResponse(map[string]any{"enabled": true, "invocation_id": invocationID, "events": events}, http.StatusOK, rw)
}
