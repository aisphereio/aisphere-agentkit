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

type PlatformApprovalsAPIRouter struct {
	controller *controllers.PlatformApprovalsAPIController
}

func NewPlatformApprovalsAPIRouter(controller *controllers.PlatformApprovalsAPIController) *PlatformApprovalsAPIRouter {
	return &PlatformApprovalsAPIRouter{controller: controller}
}

func (r *PlatformApprovalsAPIRouter) Routes() Routes {
	return Routes{
		{Name: "ListPlatformApprovals", Methods: []string{"GET"}, Pattern: "/platform/approvals", HandlerFunc: r.controller.ListApprovalsHandler},
		{Name: "CreatePlatformApproval", Methods: []string{"POST"}, Pattern: "/platform/approvals", HandlerFunc: r.controller.CreateApprovalHandler},
		{Name: "GetPlatformApproval", Methods: []string{"GET"}, Pattern: "/platform/approvals/{approval_id}", HandlerFunc: r.controller.GetApprovalHandler},
		{Name: "ApprovePlatformApproval", Methods: []string{"POST"}, Pattern: "/platform/approvals/{approval_id}/approve", HandlerFunc: r.controller.ApproveHandler},
		{Name: "RejectPlatformApproval", Methods: []string{"POST"}, Pattern: "/platform/approvals/{approval_id}/reject", HandlerFunc: r.controller.RejectHandler},
		{Name: "ExpirePlatformApproval", Methods: []string{"POST"}, Pattern: "/platform/approvals/{approval_id}/expire", HandlerFunc: r.controller.ExpireHandler},
	}
}
