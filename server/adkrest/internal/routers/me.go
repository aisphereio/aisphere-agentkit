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

// MeAPIRouter defines platform identity routes.
type MeAPIRouter struct {
	controller *controllers.MeAPIController
}

// NewMeAPIRouter creates a new MeAPIRouter.
func NewMeAPIRouter(controller *controllers.MeAPIController) *MeAPIRouter {
	return &MeAPIRouter{controller: controller}
}

// Routes returns platform identity routes.
func (r *MeAPIRouter) Routes() Routes {
	return Routes{
		Route{
			Name:        "Me",
			Methods:     []string{http.MethodGet},
			Pattern:     "/me",
			HandlerFunc: r.controller.MeHandler,
		},
	}
}
