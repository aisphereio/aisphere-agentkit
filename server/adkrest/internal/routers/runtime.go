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

// RuntimeAPIRouter defines the routes for the Runtime API.
type RuntimeAPIRouter struct {
	runtimeController *controllers.RuntimeAPIController
}

// NewRuntimeAPIRouter creates a new RuntimeAPIRouter.
func NewRuntimeAPIRouter(controller *controllers.RuntimeAPIController) *RuntimeAPIRouter {
	return &RuntimeAPIRouter{runtimeController: controller}
}

// Routes returns the routes for the Runtime API.
func (r *RuntimeAPIRouter) Routes() Routes {
	return Routes{
		Route{
			Name:        "RunAgent",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/run",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.RunHandler),
		},
		Route{
			Name:        "RunAgentSse",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/run_sse",
			HandlerFunc: r.runtimeController.RunSSEHandler,
		},
		Route{
			Name:        "ResumeAgentSse",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/run_sse/resume",
			HandlerFunc: r.runtimeController.ResumeRunSSEHandler,
		},
		Route{
			Name:        "SubAgentTaskEvents",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/subagent_tasks",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.SubAgentTaskEventsHandler),
		},
		Route{
			Name:        "RuntimeEvents",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/runtime_events",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.RuntimeEventsHandler),
		},
		Route{
			Name:        "SessionWorkspaceList",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/session_workspace",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.SessionWorkspaceListHandler),
		},
		Route{
			Name:        "SessionWorkspaceRead",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/session_workspace/read",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.SessionWorkspaceReadHandler),
		},
		Route{
			Name:        "BusinessLogStream",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/business_logs/stream",
			HandlerFunc: r.runtimeController.BusinessLogStreamHandler,
		},
		Route{
			Name:        "CancelAgentRun",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/run_sse/cancel",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.CancelRunHandler),
		},
		Route{
			Name:        "RunAgentLive",
			Methods:     []string{http.MethodGet, http.MethodOptions},
			Pattern:     "/run_live",
			HandlerFunc: controllers.NewErrorHandler(r.runtimeController.RunLiveHandler),
		},
	}
}
