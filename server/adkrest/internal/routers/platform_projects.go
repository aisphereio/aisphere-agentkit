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

type PlatformProjectsAPIRouter struct {
	controller *controllers.PlatformProjectsAPIController
}

func NewPlatformProjectsAPIRouter(controller *controllers.PlatformProjectsAPIController) *PlatformProjectsAPIRouter {
	return &PlatformProjectsAPIRouter{controller: controller}
}

func (r *PlatformProjectsAPIRouter) Routes() Routes {
	return Routes{
		{Name: "ListProjectWorkspaces", Methods: []string{"GET"}, Pattern: "/platform/project-workspaces", HandlerFunc: r.controller.ListProjectWorkspacesHandler},
		{Name: "ListPlatformProjects", Methods: []string{"GET"}, Pattern: "/platform/projects", HandlerFunc: r.controller.ListProjectsHandler},
		{Name: "CreatePlatformProject", Methods: []string{"POST"}, Pattern: "/platform/projects", HandlerFunc: r.controller.CreateProjectHandler},
		{Name: "GetPlatformProject", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}", HandlerFunc: r.controller.GetProjectHandler},
		{Name: "UpdatePlatformProject", Methods: []string{"PATCH"}, Pattern: "/platform/projects/{project_id}", HandlerFunc: r.controller.UpdateProjectHandler},
		{Name: "DeletePlatformProject", Methods: []string{"DELETE"}, Pattern: "/platform/projects/{project_id}", HandlerFunc: r.controller.DeleteProjectHandler},
		{Name: "ArchivePlatformProject", Methods: []string{"POST"}, Pattern: "/platform/projects/{project_id}/archive", HandlerFunc: r.controller.ArchiveProjectHandler},
		{Name: "ListProjectArtifacts", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/artifacts", HandlerFunc: r.controller.ListProjectArtifactsHandler},
		{Name: "GetProjectArtifact", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/artifacts/{artifact_id}", HandlerFunc: r.controller.GetProjectArtifactHandler},
		{Name: "UpdateProjectArtifact", Methods: []string{"PATCH"}, Pattern: "/platform/projects/{project_id}/artifacts/{artifact_id}", HandlerFunc: r.controller.UpdateProjectArtifactHandler},
		{Name: "DeleteProjectArtifact", Methods: []string{"DELETE"}, Pattern: "/platform/projects/{project_id}/artifacts/{artifact_id}", HandlerFunc: r.controller.DeleteProjectArtifactHandler},
		{Name: "LoadProjectArtifactContent", Methods: []string{"GET"}, Pattern: "/platform/projects/{project_id}/artifacts/{artifact_id}/content", HandlerFunc: r.controller.LoadProjectArtifactContentHandler},
	}
}
