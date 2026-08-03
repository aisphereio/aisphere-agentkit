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

type PlatformUsersAPIRouter struct {
	controller *controllers.PlatformUsersAPIController
}

func NewPlatformUsersAPIRouter(controller *controllers.PlatformUsersAPIController) *PlatformUsersAPIRouter {
	return &PlatformUsersAPIRouter{controller: controller}
}

func (r *PlatformUsersAPIRouter) Routes() Routes {
	return Routes{
		{Name: "GetCurrentPlatformTenant", Methods: []string{"GET"}, Pattern: "/platform/tenant", HandlerFunc: r.controller.GetCurrentTenantHandler},
		{Name: "CreatePlatformTenant", Methods: []string{"POST"}, Pattern: "/platform/tenants", HandlerFunc: r.controller.CreateTenantHandler},
		{Name: "ListPlatformUsers", Methods: []string{"GET"}, Pattern: "/platform/users", HandlerFunc: r.controller.ListUsersHandler},
		{Name: "CreatePlatformUser", Methods: []string{"POST"}, Pattern: "/platform/users", HandlerFunc: r.controller.CreateUserHandler},
		{Name: "GetPlatformUser", Methods: []string{"GET"}, Pattern: "/platform/users/{user_id}", HandlerFunc: r.controller.GetUserHandler},
		{Name: "UpdatePlatformUser", Methods: []string{"PATCH"}, Pattern: "/platform/users/{user_id}", HandlerFunc: r.controller.UpdateUserHandler},
		{Name: "DeletePlatformUser", Methods: []string{"DELETE"}, Pattern: "/platform/users/{user_id}", HandlerFunc: r.controller.DeleteUserHandler},
	}
}
