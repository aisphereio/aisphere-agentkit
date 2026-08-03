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

import "google.golang.org/adk/server/adkrest/controllers"

type PlatformRunsAPIRouter struct {
	controller *controllers.PlatformRunsAPIController
}

func NewPlatformRunsAPIRouter(controller *controllers.PlatformRunsAPIController) *PlatformRunsAPIRouter {
	return &PlatformRunsAPIRouter{controller: controller}
}

func (r *PlatformRunsAPIRouter) Routes() Routes {
	return Routes{
		{Name: "ListPlatformRuns", Methods: []string{"GET"}, Pattern: "/platform/runs", HandlerFunc: r.controller.ListRunsHandler},
		{Name: "CreatePlatformRun", Methods: []string{"POST"}, Pattern: "/platform/runs", HandlerFunc: r.controller.CreateRunHandler},
		{Name: "GetPlatformRun", Methods: []string{"GET"}, Pattern: "/platform/runs/{run_id}", HandlerFunc: r.controller.GetRunHandler},
		{Name: "UpdatePlatformRun", Methods: []string{"PATCH"}, Pattern: "/platform/runs/{run_id}", HandlerFunc: r.controller.UpdateRunHandler},
		{Name: "ListPlatformRunSteps", Methods: []string{"GET"}, Pattern: "/platform/runs/{run_id}/steps", HandlerFunc: r.controller.ListStepsHandler},
		{Name: "CreatePlatformRunStep", Methods: []string{"POST"}, Pattern: "/platform/runs/{run_id}/steps", HandlerFunc: r.controller.CreateStepHandler},
		{Name: "UpdatePlatformRunStep", Methods: []string{"PATCH"}, Pattern: "/platform/run-steps/{step_id}", HandlerFunc: r.controller.UpdateStepHandler},
	}
}
