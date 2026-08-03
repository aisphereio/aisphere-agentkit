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

package routers

import (
	"net/http"

	"google.golang.org/adk/server/adkrest/controllers"
)

type RuntimeTraceAPIRouter struct {
	controller *controllers.RuntimeTraceAPIController
}

func NewRuntimeTraceAPIRouter(controller *controllers.RuntimeTraceAPIController) *RuntimeTraceAPIRouter {
	return &RuntimeTraceAPIRouter{controller: controller}
}

func (r *RuntimeTraceAPIRouter) Routes() Routes {
	return Routes{
		Route{Name: "ListRuntimeTraces", Methods: []string{http.MethodGet}, Pattern: "/runtime/traces", HandlerFunc: r.controller.ListTracesHandler},
		Route{Name: "GetRuntimeTrace", Methods: []string{http.MethodGet}, Pattern: "/runtime/traces/{invocation_id}", HandlerFunc: r.controller.GetTraceHandler},
	}
}
