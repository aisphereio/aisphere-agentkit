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

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/server/adkrest/internal/models"
	"google.golang.org/adk/session"
)

// TODO: Confirm error handling and target semantic for REST API.

// SessionsAPIController is the controller for the Sessions API.
type SessionsAPIController struct {
	service       session.Service
	subAgentStore SubAgentTaskObserveStore
	nativeManager *sessionnative.Manager
}

// NewSessionsAPIController creates a new SessionsAPIController.
func NewSessionsAPIController(service session.Service, extras ...any) *SessionsAPIController {
	var subAgentStore SubAgentTaskObserveStore
	var nativeManager *sessionnative.Manager
	for _, extra := range extras {
		switch v := extra.(type) {
		case SubAgentTaskObserveStore:
			subAgentStore = v
		case *sessionnative.Manager:
			nativeManager = v
		}
	}
	return &SessionsAPIController{service: service, subAgentStore: subAgentStore, nativeManager: nativeManager}
}

// CreateSesssionHTTP is a HTTP handler for the create session API.
func (c *SessionsAPIController) CreateSessionHandler(rw http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	sessionID, err := models.SessionIDFromHTTPParameters(params)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	createSessionRequest := models.CreateSessionRequest{}
	// No state and no events, fails to decode req.Body failing with "EOF"
	if req.ContentLength > 0 {
		err := json.NewDecoder(req.Body).Decode(&createSessionRequest)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if sessionID.ID == "" && c.nativeManager != nil && c.nativeManager.Enabled() {
		sessionID.ID = "sess_" + uuid.NewString()
	}
	projectID := c.projectIDFromRequest(req.Context(), req, sessionID)
	if projectID != "" {
		if createSessionRequest.State == nil {
			createSessionRequest.State = map[string]any{}
		}
		if _, ok := createSessionRequest.State["project_id"]; !ok {
			createSessionRequest.State["project_id"] = projectID
		}
		if _, ok := createSessionRequest.State["projectId"]; !ok {
			createSessionRequest.State["projectId"] = projectID
		}
	}
	if c.nativeManager != nil && c.nativeManager.Enabled() {
		ctx := aihubruntime.WithRequestHeaders(aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")), req.Header)
		lease, err := c.nativeManager.CreateSession(ctx, sessionnative.CreateSessionRequest{
			AppName: sessionID.AppName, UserID: sessionID.UserID, SessionID: sessionID.ID, ProjectID: projectID,
			AgentID: sessionID.AppName, State: createSessionRequest.State, SkipAgentResolve: true, Reuse: true,
		})
		if err != nil {
			http.Error(rw, "failed to create native sandbox session: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if createSessionRequest.State == nil {
			createSessionRequest.State = map[string]any{}
		}
		for key, value := range lease.StateDelta() {
			createSessionRequest.State[key] = value
		}
	}
	respSession, err := c.createSession(req.Context(), sessionID, createSessionRequest)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(respSession, http.StatusOK, rw)
}

func (c *SessionsAPIController) createSession(ctx context.Context, sessionID models.SessionID, createSessionRequest models.CreateSessionRequest) (models.Session, error) {
	session, err := c.service.Create(ctx, &session.CreateRequest{
		AppName:   sessionID.AppName,
		UserID:    sessionID.UserID,
		SessionID: sessionID.ID,
		State:     createSessionRequest.State,
	})
	if err != nil {
		return models.Session{}, err
	}
	for _, event := range createSessionRequest.Events {
		err = c.service.AppendEvent(ctx, session.Session, models.ToSessionEvent(event))
		if err != nil {
			return models.Session{}, err
		}
	}
	return models.FromSession(session.Session)
}

// DeleteSession handles deleting a specific session.
func (c *SessionsAPIController) DeleteSessionHandler(rw http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	sessionID, err := models.SessionIDFromHTTPParameters(params)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if sessionID.ID == "" {
		http.Error(rw, "session_id parameter is required", http.StatusBadRequest)
		return
	}

	err = c.service.Delete(req.Context(), &session.DeleteRequest{
		AppName:   sessionID.AppName,
		UserID:    sessionID.UserID,
		SessionID: sessionID.ID,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if c.subAgentStore != nil {
		c.subAgentStore.DeleteSession(req.Context(), sessionID.AppName, sessionID.UserID, sessionID.ID)
	}
	EncodeJSONResponse(nil, http.StatusOK, rw)
}

// GetSession retrieves a specific session by its ID.
func (c *SessionsAPIController) GetSessionHandler(rw http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	sessionID, err := models.SessionIDFromHTTPParameters(params)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if sessionID.ID == "" {
		http.Error(rw, "session_id parameter is required", http.StatusBadRequest)
		return
	}
	storedSession, err := c.service.Get(req.Context(), &session.GetRequest{
		AppName:   sessionID.AppName,
		UserID:    sessionID.UserID,
		SessionID: sessionID.ID,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	session, err := models.FromSession(storedSession.Session)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(session, http.StatusOK, rw)
}

// ListSessions handles listing all sessions for a given app and user.
func (c *SessionsAPIController) ListSessionsHandler(rw http.ResponseWriter, req *http.Request) {
	params := mux.Vars(req)
	sessionID, err := models.SessionIDFromHTTPParameters(params)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	var sessions []models.Session
	resp, err := c.service.List(req.Context(), &session.ListRequest{
		AppName: sessionID.AppName,
		UserID:  sessionID.UserID,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	projectID := c.projectIDFromRequest(req.Context(), req, sessionID)
	for _, session := range resp.Sessions {
		respSession, err := models.FromSession(session)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		if projectID != "" && !sessionMatchesProject(respSession, projectID) {
			continue
		}
		sessions = append(sessions, respSession)
	}
	EncodeJSONResponse(sessions, http.StatusOK, rw)
}

// ListAllSessionsHandler handles listing sessions across apps/users for the admin console.
func (c *SessionsAPIController) ListAllSessionsHandler(rw http.ResponseWriter, req *http.Request) {
	global, ok := c.service.(session.GlobalService)
	if !ok {
		http.Error(rw, "global session listing is not supported by this session backend", http.StatusNotImplemented)
		return
	}
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	resp, err := global.ListAll(req.Context(), &session.ListAllRequest{
		AppName: q.Get("app_name"),
		UserID:  q.Get("user_id"),
		Limit:   limit,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	sessions := make([]models.Session, 0, len(resp.Sessions))
	for _, sess := range resp.Sessions {
		respSession, err := models.FromSession(sess)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		if projectID := strings.TrimSpace(q.Get("project_id")); projectID != "" && !sessionMatchesProject(respSession, projectID) {
			continue
		}
		sessions = append(sessions, respSession)
	}
	EncodeJSONResponse(sessions, http.StatusOK, rw)
}

// GetSessionByIDHandler handles fetching a session without requiring app_name/user_id.
func (c *SessionsAPIController) GetSessionByIDHandler(rw http.ResponseWriter, req *http.Request) {
	global, ok := c.service.(session.GlobalService)
	if !ok {
		http.Error(rw, "global session lookup is not supported by this session backend", http.StatusNotImplemented)
		return
	}
	sessionID := mux.Vars(req)["session_id"]
	if sessionID == "" {
		http.Error(rw, "session_id parameter is required", http.StatusBadRequest)
		return
	}
	storedSession, err := global.GetByID(req.Context(), &session.GetByIDRequest{SessionID: sessionID})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	respSession, err := models.FromSession(storedSession.Session)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(respSession, http.StatusOK, rw)
}

func (c *SessionsAPIController) projectIDFromRequest(ctx context.Context, req *http.Request, sessionID models.SessionID) string {
	q := req.URL.Query()
	if projectID := strings.TrimSpace(q.Get("project_id")); projectID != "" {
		return projectID
	}
	currentSessionID := strings.TrimSpace(q.Get("current_session_id"))
	if currentSessionID == "" {
		return ""
	}
	resp, err := c.service.Get(ctx, &session.GetRequest{
		AppName:   sessionID.AppName,
		UserID:    sessionID.UserID,
		SessionID: currentSessionID,
	})
	if err != nil {
		return ""
	}
	current, err := models.FromSession(resp.Session)
	if err != nil {
		return ""
	}
	return projectIDFromSession(current)
}

func sessionMatchesProject(sess models.Session, projectID string) bool {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return true
	}
	for _, key := range []string{"project_id", "projectId"} {
		if valueMatchesProject(sess.State[key], projectID) {
			return true
		}
	}
	return false
}

func projectIDFromSession(sess models.Session) string {
	for _, key := range []string{"project_id", "projectId"} {
		if projectID := stringProjectID(sess.State[key]); projectID != "" {
			return projectID
		}
	}
	return ""
}

func stringProjectID(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

func valueMatchesProject(value any, projectID string) bool {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) == projectID
	case []string:
		for _, item := range v {
			if strings.TrimSpace(item) == projectID {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if valueMatchesProject(item, projectID) {
				return true
			}
		}
	}
	return false
}
