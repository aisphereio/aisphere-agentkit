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

type PlatformImprovementsAPIRouter struct {
	controller *controllers.PlatformImprovementsAPIController
}

func NewPlatformImprovementsAPIRouter(controller *controllers.PlatformImprovementsAPIController) *PlatformImprovementsAPIRouter {
	return &PlatformImprovementsAPIRouter{controller: controller}
}

func (r *PlatformImprovementsAPIRouter) Routes() Routes {
	return Routes{
		{Name: "ListPlatformImprovementIssues", Methods: []string{"GET"}, Pattern: "/platform/improvement-issues", HandlerFunc: r.controller.ListIssuesHandler},
		{Name: "CreatePlatformImprovementIssue", Methods: []string{"POST"}, Pattern: "/platform/improvement-issues", HandlerFunc: r.controller.CreateIssueHandler},
		{Name: "GetPlatformImprovementIssue", Methods: []string{"GET"}, Pattern: "/platform/improvement-issues/{issue_id}", HandlerFunc: r.controller.GetIssueHandler},
		{Name: "UpdatePlatformImprovementIssue", Methods: []string{"PATCH"}, Pattern: "/platform/improvement-issues/{issue_id}", HandlerFunc: r.controller.UpdateIssueHandler},
		{Name: "ListPlatformImprovementProposals", Methods: []string{"GET"}, Pattern: "/platform/improvement-proposals", HandlerFunc: r.controller.ListProposalsHandler},
		{Name: "CreatePlatformImprovementProposal", Methods: []string{"POST"}, Pattern: "/platform/improvement-proposals", HandlerFunc: r.controller.CreateProposalHandler},
		{Name: "GetPlatformImprovementProposal", Methods: []string{"GET"}, Pattern: "/platform/improvement-proposals/{proposal_id}", HandlerFunc: r.controller.GetProposalHandler},
		{Name: "ApprovePlatformImprovementProposal", Methods: []string{"POST"}, Pattern: "/platform/improvement-proposals/{proposal_id}/approve", HandlerFunc: r.controller.ApproveProposalHandler},
		{Name: "RejectPlatformImprovementProposal", Methods: []string{"POST"}, Pattern: "/platform/improvement-proposals/{proposal_id}/reject", HandlerFunc: r.controller.RejectProposalHandler},
		{Name: "MarkPlatformImprovementProposalApplied", Methods: []string{"POST"}, Pattern: "/platform/improvement-proposals/{proposal_id}/mark-applied", HandlerFunc: r.controller.MarkProposalAppliedHandler},
	}
}
