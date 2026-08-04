// Package skillruntime assembles the Hub-pinned skill set for an in-process
// AgentKit runner. Skills remain files; the toolset is the context bridge that
// exposes their metadata and controlled resource loading to ADK.
package skillruntime

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/internal/runtimeplan"
	"google.golang.org/adk/internal/skillservice"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/skilltoolset"
)

func NewToolset(ctx context.Context, root string, bindings []runtimeplan.SkillBinding) (tool.Toolset, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("runtime skill root is required")
	}
	selected := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if name := strings.TrimSpace(binding.Name); name != "" {
			selected = append(selected, name)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	service, err := skillservice.NewFileSystemService(root)
	if err != nil {
		return nil, fmt.Errorf("create runtime skill service: %w", err)
	}
	source, err := service.Source(ctx, selected, "complete")
	if err != nil {
		return nil, fmt.Errorf("load runtime skill source: %w", err)
	}
	frontmatters, err := source.ListFrontmatters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list runtime skills: %w", err)
	}
	if len(frontmatters) != len(selected) {
		return nil, fmt.Errorf("runtime skill snapshot requested %d skills but materialized %d", len(selected), len(frontmatters))
	}
	set, err := skilltoolset.New(ctx, skilltoolset.Config{Source: source})
	if err != nil {
		return nil, fmt.Errorf("create runtime skill toolset: %w", err)
	}
	return set, nil
}
