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
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/tool/envmanagertool"
)

// SubAgentTaskEventsHandler returns persisted sub-agent task runtime events for a session.
func (c *RuntimeAPIController) SubAgentTaskEventsHandler(rw http.ResponseWriter, req *http.Request) error {
	return c.runtimeEventsHandler(rw, req, true)
}

// RuntimeEventsHandler returns persisted runtime observation events for a session.
func (c *RuntimeAPIController) RuntimeEventsHandler(rw http.ResponseWriter, req *http.Request) error {
	return c.runtimeEventsHandler(rw, req, false)
}

func (c *RuntimeAPIController) runtimeEventsHandler(rw http.ResponseWriter, req *http.Request, subAgentOnlyDefault bool) error {
	if c.subAgentStore == nil {
		EncodeJSONResponse(map[string]any{"events": []any{}}, http.StatusOK, rw)
		return nil
	}
	q := req.URL.Query()
	appName := strings.TrimSpace(q.Get("app_name"))
	userID := strings.TrimSpace(q.Get("user_id"))
	sessionID := strings.TrimSpace(q.Get("session_id"))
	scope := strings.ToLower(strings.TrimSpace(q.Get("scope")))
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	events := c.subAgentStore.List(req.Context(), appName, userID, sessionID)
	if events == nil {
		events = []map[string]any{}
	}
	if scope == "subagent" || (scope == "" && subAgentOnlyDefault) {
		filtered := make([]map[string]any, 0, len(events))
		for _, event := range events {
			if strings.HasPrefix(runtimeStoredEventType(event), "subagent.task.") {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	} else if scope == "errors" || scope == "error" {
		filtered := make([]map[string]any, 0, len(events))
		for _, event := range events {
			if runtimeStoredEventIsError(event) {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}
	EncodeJSONResponse(map[string]any{"events": events}, http.StatusOK, rw)
	return nil
}

func runtimeStoredEventType(event map[string]any) string {
	if event == nil {
		return ""
	}
	if s := strings.TrimSpace(anyString(event["event_type"])); s != "" {
		return s
	}
	if s := strings.TrimSpace(anyString(event["eventType"])); s != "" {
		return s
	}
	if data, ok := event["data"].(map[string]any); ok {
		if s := strings.TrimSpace(anyString(data["event_type"])); s != "" {
			return s
		}
		if s := strings.TrimSpace(anyString(data["eventType"])); s != "" {
			return s
		}
	}
	return strings.TrimSpace(anyString(event["type"]))
}

func runtimeStoredEventIsError(event map[string]any) bool {
	if event == nil {
		return false
	}
	eventType := runtimeStoredEventType(event)
	switch eventType {
	case runtimetrace.EventInvocationFailed,
		runtimetrace.EventAgentError,
		runtimetrace.EventModelCallError,
		runtimetrace.EventToolError,
		runtimetrace.EventSkillError,
		runtimetrace.EventSubAgentTaskFailed:
		return true
	}
	if strings.Contains(strings.ToLower(eventType), ".error") || strings.Contains(strings.ToLower(eventType), ".failed") {
		return true
	}
	if data, ok := event["data"].(map[string]any); ok {
		if strings.TrimSpace(anyString(data["error"])) != "" ||
			strings.TrimSpace(anyString(data["error_message"])) != "" ||
			strings.TrimSpace(anyString(data["error_code"])) != "" {
			return true
		}
	}
	return false
}

// BusinessLogStreamHandler streams real business logs (Docker/K8s/file/journal) as SSE.
// It is intentionally separate from RuntimeEvent: this endpoint exposes live
// workload logs requested by the user, not durable Agent execution facts.
func (c *RuntimeAPIController) BusinessLogStreamHandler(rw http.ResponseWriter, req *http.Request) {
	rc := http.NewResponseController(rw)
	if err := clearSSEWriteDeadline(rc); err != nil {
		http.Error(rw, "failed to clear write deadline: "+err.Error(), http.StatusInternalServerError)
		return
	}
	setSSEHeaders(rw)
	if err := flushSSE(rc, c.sseTimeout); err != nil {
		http.Error(rw, "failed to flush headers", http.StatusInternalServerError)
		return
	}

	q := req.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	follow := q.Get("follow") == "1" || q.Get("follow") == "true" || q.Get("follow") == "yes"
	blogReq := envmanagertool.BusinessLogRequest{
		EnvironmentID: q.Get("environment_id"),
		Kind:          q.Get("kind"),
		Container:     q.Get("container"),
		Namespace:     q.Get("namespace"),
		Pod:           q.Get("pod"),
		K8sContainer:  q.Get("k8s_container"),
		Path:          q.Get("path"),
		Unit:          q.Get("unit"),
		Tail:          tail,
		Follow:        follow,
	}

	configPath := c.defaultBusinessLogEnvConfigPath()
	cfg := envmanagertool.Config{
		ConfigPath:            configPath,
		DefaultSafetyMode:     envmanagertool.SafetyModeSafeApproval,
		DefaultFreedomLevel:   envmanagertool.FreedomF2,
		DefaultMaxOutputBytes: 64 * 1024,
		DefaultTimeoutSeconds: 0,
		AllowLocal:            false,
		DryRunDefault:         false,
	}

	emit := func(event envmanagertool.BusinessLogEvent) error {
		data, err := envmanagertool.EncodeBusinessLogEvent(event)
		if err != nil {
			return err
		}
		return flashNamedEventWithID(rc, rw, event.Type, "", data, c.sseTimeout)
	}
	if err := envmanagertool.StreamBusinessLogs(req.Context(), cfg, blogReq, emit); err != nil {
		log.Printf("business log stream failed: %v", err)
	}
}

func (c *RuntimeAPIController) defaultBusinessLogEnvConfigPath() string {
	if c.runtimeConfig == nil {
		return filepath.Clean("agents/env_manager/env/environments.example.json")
	}
	appsRoot := c.runtimeConfig.Builder.AppsRoot
	if appsRoot == "" {
		appsRoot = "./agents"
	}
	return filepath.Clean(filepath.Join(appsRoot, "env_manager", "env", "environments.example.json"))
}
