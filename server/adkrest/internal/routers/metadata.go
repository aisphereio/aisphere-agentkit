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

// MetadataAPIRouter defines runtime metadata routes for the WebUI Builder.
type MetadataAPIRouter struct {
	metadataController *controllers.MetadataAPIController
}

// NewMetadataAPIRouter creates a new MetadataAPIRouter.
func NewMetadataAPIRouter(controller *controllers.MetadataAPIController) *MetadataAPIRouter {
	return &MetadataAPIRouter{metadataController: controller}
}

// Routes returns runtime metadata routes.
func (r *MetadataAPIRouter) Routes() Routes {
	return Routes{
		Route{
			Name:        "Version",
			Methods:     []string{http.MethodGet},
			Pattern:     "/version",
			HandlerFunc: r.metadataController.VersionHandler,
		},
		Route{
			Name:        "ListModels",
			Methods:     []string{http.MethodGet},
			Pattern:     "/models",
			HandlerFunc: r.metadataController.ModelsHandler,
		},
		Route{
			Name:        "ListTools",
			Methods:     []string{http.MethodGet},
			Pattern:     "/tools",
			HandlerFunc: r.metadataController.ToolsHandler,
		},
		Route{
			Name:        "BuilderDefaults",
			Methods:     []string{http.MethodGet},
			Pattern:     "/builder/defaults",
			HandlerFunc: r.metadataController.BuilderDefaultsHandler,
		},
		Route{
			Name:        "UploadConfig",
			Methods:     []string{http.MethodGet},
			Pattern:     "/uploads/config",
			HandlerFunc: r.metadataController.UploadsHandler,
		},
	}
}
