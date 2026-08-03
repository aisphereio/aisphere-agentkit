package novelstore

import (
	"strings"
	"testing"
)

func TestSplitTextLeadingContentDrop(t *testing.T) {
	text := "作品简介\n这是一段简介。\n\n第一章 纨绔\n他站在门口。\n\n第二章 花魁\n她抬眼看去。"

	parts, warnings, _, err := splitText(text, nil, 1, LeadingContentDrop)
	if err != nil {
		t.Fatalf("splitText returned error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2", len(parts))
	}
	if parts[0].Title != "第一章 纨绔" {
		t.Fatalf("first title = %q, want 第一章 纨绔", parts[0].Title)
	}
	if strings.Contains(parts[0].Text, "作品简介") {
		t.Fatalf("first chapter unexpectedly contains dropped leading content: %q", parts[0].Text)
	}
	if len(warnings) == 0 {
		t.Fatalf("warnings is empty, want dropped leading content warning")
	}
}

func TestSplitTextLeadingContentKeep(t *testing.T) {
	text := "作品简介\n这是一段简介。\n\n第一章 纨绔\n他站在门口。\n\n第二章 花魁\n她抬眼看去。"

	parts, _, _, err := splitText(text, nil, 1, LeadingContentKeep)
	if err != nil {
		t.Fatalf("splitText returned error: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("len(parts) = %d, want 3", len(parts))
	}
	if parts[0].Title != "前置内容" {
		t.Fatalf("first title = %q, want 前置内容", parts[0].Title)
	}
}

func TestSplitTextLeadingContentMerge(t *testing.T) {
	text := "作品简介\n这是一段简介。\n\n第一章 纨绔\n他站在门口。\n\n第二章 花魁\n她抬眼看去。"

	parts, _, _, err := splitText(text, nil, 1, LeadingContentMerge)
	if err != nil {
		t.Fatalf("splitText returned error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2", len(parts))
	}
	if parts[0].Title != "第一章 纨绔" {
		t.Fatalf("first title = %q, want 第一章 纨绔", parts[0].Title)
	}
	if !strings.Contains(parts[0].Text, "作品简介") {
		t.Fatalf("first chapter does not contain merged leading content: %q", parts[0].Text)
	}
}
