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

// BuilderAPIRouter defines the routes used by embedded ADK WebUI builder.
type BuilderAPIRouter struct {
	builderController *controllers.BuilderAPIController
}

// NewBuilderAPIRouter creates a new BuilderAPIRouter.
func NewBuilderAPIRouter(controller *controllers.BuilderAPIController) *BuilderAPIRouter {
	return &BuilderAPIRouter{builderController: controller}
}

// Routes returns builder and dev graph routes.
func (r *BuilderAPIRouter) Routes() Routes {
	return Routes{
		Route{
			Name:        "BuilderSave",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/builder/save",
			HandlerFunc: r.builderController.SaveHandler,
		},
		Route{
			Name:        "BuilderGetApp",
			Methods:     []string{http.MethodGet},
			Pattern:     "/builder/app/{app}",
			HandlerFunc: r.builderController.GetAppHandler,
		},
		Route{
			Name:        "BuilderSaveAppFile",
			Methods:     []string{http.MethodPut, http.MethodPost, http.MethodOptions},
			Pattern:     "/builder/app/{app}/file/{file_path:.*}",
			HandlerFunc: r.builderController.SaveAppFileHandler,
		},
		Route{
			Name:        "BuilderCancelApp",
			Methods:     []string{http.MethodPost, http.MethodOptions},
			Pattern:     "/builder/app/{app}/cancel",
			HandlerFunc: r.builderController.CancelHandler,
		},
		Route{
			Name:        "DevBuildGraph",
			Methods:     []string{http.MethodGet},
			Pattern:     "/dev/build_graph/{app}",
			HandlerFunc: r.builderController.BuildGraphHandler,
		},
		Route{
			Name:        "DevBuildGraphImage",
			Methods:     []string{http.MethodGet},
			Pattern:     "/dev/build_graph_image/{app}",
			HandlerFunc: r.builderController.BuildGraphImageHandler,
		},
	}
}
