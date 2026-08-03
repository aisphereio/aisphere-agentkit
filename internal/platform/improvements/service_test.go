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

package improvements

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestImprovementProposalLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	svc := NewService(db)
	ctx := context.Background()

	issue, err := svc.CreateIssue(ctx, CreateIssueRequest{
		TenantID:     "default",
		RunID:        "run_1",
		AppName:      "novel_pipeline",
		AgentName:    "chapter_review_agent",
		IssueType:    "skill_gap",
		Severity:     "high",
		Title:        "审稿缺少商业节奏检查",
		Description:  "review 输出没有 rhythm_check 字段。",
		EvidenceJSON: `{"artifact":"chapter_current_review.json"}`,
		CreatedBy:    "objective_review_agent",
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if issue.Status != IssueStatusOpen {
		t.Fatalf("issue status = %q, want %q", issue.Status, IssueStatusOpen)
	}

	proposal, err := svc.CreateProposal(ctx, CreateProposalRequest{
		TenantID:       "default",
		SourceIssueID:  issue.ID,
		RunID:          "run_1",
		AppName:        "novel_pipeline",
		Title:          "增强章节审稿 Skill",
		ProposalType:   "update_skill",
		RiskLevel:      "medium",
		CreatedByAgent: "self_improvement_agent",
		Changes: []CreateChangeRequest{{
			ChangeType: "skill_patch",
			TargetPath: "skills/novel-chapter-review/SKILL.md",
			DiffText:   "+ rhythm_check",
		}},
	})
	if err != nil {
		t.Fatalf("CreateProposal() error = %v", err)
	}
	if proposal.Proposal.Status != ProposalStatusPendingReview {
		t.Fatalf("proposal status = %q, want %q", proposal.Proposal.Status, ProposalStatusPendingReview)
	}
	if len(proposal.Changes) != 1 {
		t.Fatalf("proposal changes len = %d, want 1", len(proposal.Changes))
	}

	updatedIssue, err := svc.GetIssue(ctx, "default", issue.ID)
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if updatedIssue.Status != IssueStatusProposed {
		t.Fatalf("updated issue status = %q, want %q", updatedIssue.Status, IssueStatusProposed)
	}

	approved, err := svc.DecideProposal(ctx, "default", proposal.Proposal.ID, DecideProposalRequest{
		Status:   ProposalStatusApproved,
		Reason:   "证据充分，修改范围清楚。",
		Reviewer: "admin",
	})
	if err != nil {
		t.Fatalf("DecideProposal() error = %v", err)
	}
	if approved.Proposal.Status != ProposalStatusApproved {
		t.Fatalf("approved status = %q, want %q", approved.Proposal.Status, ProposalStatusApproved)
	}
	if approved.Changes[0].Status != ChangeStatusApproved {
		t.Fatalf("change status = %q, want %q", approved.Changes[0].Status, ChangeStatusApproved)
	}

	applied, err := svc.MarkProposalApplied(ctx, "default", proposal.Proposal.ID, ApplyProposalRequest{
		AppliedBy:       "admin",
		ApplyResultJSON: `{"result":"manual_patch_applied"}`,
	})
	if err != nil {
		t.Fatalf("MarkProposalApplied() error = %v", err)
	}
	if applied.Proposal.Status != ProposalStatusApplied {
		t.Fatalf("applied status = %q, want %q", applied.Proposal.Status, ProposalStatusApplied)
	}
	if applied.Changes[0].Status != ChangeStatusApplied {
		t.Fatalf("applied change status = %q, want %q", applied.Changes[0].Status, ChangeStatusApplied)
	}
}

func TestRejectProposalPreventsApply(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	svc := NewService(db)
	ctx := context.Background()

	proposal, err := svc.CreateProposal(ctx, CreateProposalRequest{
		TenantID:     "default",
		Title:        "危险的工具修改",
		ProposalType: "update_tool",
		RiskLevel:    "high",
	})
	if err != nil {
		t.Fatalf("CreateProposal() error = %v", err)
	}
	if _, err := svc.DecideProposal(ctx, "default", proposal.Proposal.ID, DecideProposalRequest{
		Status:   ProposalStatusRejected,
		Reason:   "风险说明不足。",
		Reviewer: "admin",
	}); err != nil {
		t.Fatalf("DecideProposal() error = %v", err)
	}
	if _, err := svc.MarkProposalApplied(ctx, "default", proposal.Proposal.ID, ApplyProposalRequest{AppliedBy: "admin"}); err == nil {
		t.Fatalf("MarkProposalApplied() error = nil, want error")
	}
}
