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
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adkrest/internal/models"
)

// requestScopedAgentLoader is retained only for the realtime ADK protocol.
// Normal text execution resolves immutable Hub plans through sessionnative.
type requestScopedAgentLoader interface {
	LoadAgentForRequest(ctx context.Context, name, sessionID string) (agent.Agent, error)
}

func (c *RuntimeAPIController) getLiveRunner(ctx context.Context, req models.RunAgentRequest) (*runner.Runner, error) {
	var curAgent agent.Agent
	var err error
	if loader, ok := c.agentLoader.(requestScopedAgentLoader); ok {
		curAgent, err = loader.LoadAgentForRequest(ctx, req.AppName, req.SessionId)
	} else {
		curAgent, err = c.agentLoader.LoadAgent(req.AppName)
	}
	if err != nil {
		return nil, newStatusError(fmt.Errorf("failed to load realtime agent: %w", err), http.StatusInternalServerError)
	}

	r, err := runner.New(runner.Config{
		AppName:           req.AppName,
		Agent:             curAgent,
		SessionService:    c.sessionService,
		MemoryService:     c.memoryService,
		ArtifactService:   c.artifactService,
		PluginConfig:      c.pluginConfig,
		AutoCreateSession: c.autoCreateSession,
	})
	if err != nil {
		return nil, newStatusError(fmt.Errorf("failed to create realtime runner: %w", err), http.StatusInternalServerError)
	}
	return r, nil
}

// RunLiveHandler is an independent realtime/WebSocket protocol. It is not a
// fallback execution path for /run or /run_sse and must converge to the same
// execution-fact contract before it can be considered production-equivalent.
func (c *RuntimeAPIController) RunLiveHandler(rw http.ResponseWriter, req *http.Request) error {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	q := req.URL.Query()
	appName := q.Get("appName")
	if appName == "" {
		appName = q.Get("app_name")
	}
	userID := q.Get("userId")
	if userID == "" {
		userID = q.Get("user_id")
	}
	sessionID := q.Get("sessionId")
	if sessionID == "" {
		sessionID = q.Get("session_id")
	}

	if appName == "" || userID == "" || sessionID == "" {
		return fmt.Errorf("appName, userId, and sessionId are required")
	}

	ws, err := upgrader.Upgrade(rw, req, nil)
	if err != nil {
		return fmt.Errorf("failed to upgrade to websocket: %w", err)
	}
	defer func() { _ = ws.Close() }()

	sendClose := func(code int, reason string) {
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
		_ = ws.SetReadDeadline(time.Now().Add(time.Second))
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				break
			}
		}
	}

	ctx := c.runtimeContext(aihubruntime.WithRequestHeaders(
		aihubruntime.WithCookieHeader(req.Context(), req.Header.Get("Cookie")),
		req.Header,
	))
	r, err := c.getLiveRunner(ctx, models.RunAgentRequest{AppName: appName, UserId: userID, SessionId: sessionID})
	if err != nil {
		closeReason := err.Error()
		if _, loadErr := c.agentLoader.LoadAgent(appName); loadErr != nil {
			closeReason = fmt.Sprintf("agent %s not found for original error: %v", appName, err)
		}
		log.Printf("failed to get realtime runner for app %s: %v", appName, err)
		sendClose(websocket.CloseInternalServerErr, closeReason)
		return nil
	}

	liveSession, eventIter, err := r.RunLive(req.Context(), userID, sessionID, agent.LiveRunConfig{
		MaxLLMCalls:              100,
		ResponseModalities:       []genai.Modality{genai.ModalityAudio},
		InputAudioTranscription:  &genai.AudioTranscriptionConfig{},
		OutputAudioTranscription: &genai.AudioTranscriptionConfig{},
	})
	if err != nil {
		log.Printf("RunLive failed for app %s: %v", appName, err)
		sendClose(websocket.CloseInternalServerErr, err.Error())
		return nil
	}
	defer func() { _ = liveSession.Close() }()

	go func() {
		defer func() { _ = liveSession.Close() }()
		for {
			messageType, p, err := ws.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("WebSocket read error for app %s: %v", appName, err)
				}
				break
			}

			if messageType == websocket.BinaryMessage {
				if err := liveSession.Send(agent.LiveRequest{
					RealtimeInput: &genai.Blob{MIMEType: "audio/pcm;rate=16000", Data: p},
				}); err != nil {
					log.Printf("failed to send binary realtime data for app %s: %v", appName, err)
					break
				}
				continue
			}
			if messageType != websocket.TextMessage {
				continue
			}

			var apiReq models.LiveRequest
			if err := json.Unmarshal(p, &apiReq); err != nil {
				log.Printf("failed to unmarshal realtime client message for app %s: %v", appName, err)
				continue
			}
			if apiReq.Close {
				break
			}

			liveReq := agent.LiveRequest{Content: apiReq.Content}
			if apiReq.ActivityStart != nil {
				liveReq.RealtimeInput = apiReq.ActivityStart
			} else if apiReq.ActivityEnd != nil {
				liveReq.RealtimeInput = apiReq.ActivityEnd
			} else if apiReq.Blob != nil {
				liveReq.RealtimeInput = &genai.Blob{MIMEType: apiReq.Blob.MIMEType, Data: apiReq.Blob.Data}
			}
			if err := liveSession.Send(liveReq); err != nil {
				log.Printf("failed to send realtime message for app %s: %v", appName, err)
				break
			}
		}
	}()

	for event, err := range eventIter {
		if err != nil {
			log.Printf("RunLive failed: %v", err)
			_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
			break
		}
		if err = ws.WriteJSON(models.FromSessionEvent(*event)); err != nil {
			break
		}
	}
	return nil
}
