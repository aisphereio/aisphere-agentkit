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

	"google.golang.org/adk/internal/aihubruntime"
)

// RunNativeOnlyHandler is the production non-streaming execution entrypoint.
// The legacy generic ADK Runner fallback is intentionally not reachable from
// AISphere routes: production execution requires the Native ADK-Go runtime plan.
func (c *RuntimeAPIController) RunNativeOnlyHandler(rw http.ResponseWriter, req *http.Request) error {
	if c.nativeManager == nil || !c.nativeManager.Enabled() {
		return newStatusError(fmt.Errorf("native ADK-Go runtime is required"), http.StatusServiceUnavailable)
	}
	runReq, err := decodeRequestBody(req)
	if err != nil {
		return err
	}
	if err := c.validateRunInputPolicy(runReq); err != nil {
		return err
	}
	ctx := aihubruntime.WithRequestHeaders(
		aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")),
		req.Header,
	)
	events, err := c.runNativeAgent(ctx, runReq)
	if err != nil {
		return err
	}
	EncodeJSONResponse(events, http.StatusOK, rw)
	return nil
}

// RunNativeOnlySSEHandler is the production streaming execution entrypoint.
// Durable replay is provided by /platform/runs/{run_id}/events/stream rather
// than the historical Redis resumable-run protocol.
func (c *RuntimeAPIController) RunNativeOnlySSEHandler(rw http.ResponseWriter, req *http.Request) {
	if c.nativeManager == nil || !c.nativeManager.Enabled() {
		http.Error(rw, "native ADK-Go runtime is required", http.StatusServiceUnavailable)
		return
	}

	rc := http.NewResponseController(rw)
	if err := clearSSEWriteDeadline(rc); err != nil {
		http.Error(rw, "failed to clear write deadline: "+err.Error(), http.StatusInternalServerError)
		return
	}

	runReq, err := decodeRequestBody(req)
	if err != nil {
		http.Error(rw, "failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	runReq = c.applyPlanMode(runReq)
	if err := c.validateRunInputPolicy(runReq); err != nil {
		status := http.StatusRequestEntityTooLarge
		if se, ok := err.(interface{ Status() int }); ok {
			status = se.Status()
		}
		http.Error(rw, err.Error(), status)
		return
	}
	c.runNativeAgentSSE(rw, req, runReq)
}
