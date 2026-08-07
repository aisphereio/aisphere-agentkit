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

package routers

import (
	"net/http"

	"google.golang.org/adk/server/adkrest/controllers"
)

// RuntimeAPIRouter defines the AISphere Runtime transport routes. Production
// text execution is native ADK-Go only; durable replay is exposed by the
// platform run Event Ledger router, not the legacy Redis resumable protocol.
type RuntimeAPIRouter struct {
	runtimeController *controllers.RuntimeAPIController
}

func NewRuntimeAPIRouter(controller *controllers.RuntimeAPIController) *RuntimeAPIRouter {
	return &RuntimeAPIRouter{runtimeController: controller}
}

func (r *RuntimeAPIRouter) Routes() Routes {
	return Routes{
		{
			Name:        "RunAgent",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/run",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.RunNativeOnlyHandler),
		},
		{
			Name:        "RunAgentSse",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/run_sse",
			HandlerFunc: r.runtimeController.RunNativeOnlySSEHandler,
		},
		{
			Name:        "SubAgentTaskEvents",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/subagent_tasks",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.SubAgentTaskEventsHandler),
		},
		{
			Name:        "RuntimeEvents",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/runtime_events",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.RuntimeEventsHandler),
		},
		{
			Name:        "SessionWorkspaceList",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/session_workspace",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.SessionWorkspaceListHandler),
		},
		{
			Name:        "SessionWorkspaceRead",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/session_workspace/read",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.SessionWorkspaceReadHandler),
		},
		{
			Name:        "BusinessLogStream",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/business_logs/stream",
			HandlerFunc: r.runtimeController.BusinessLogStreamHandler,
		},
		{
			Name:        "RunAgentLive",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/run_live",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.RunLiveHandler),
		},
	}
}
