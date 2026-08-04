// Package permissiongate enforces the Hub-authorized RuntimePlan at execution
// time. Hub resolves the capability snapshot; Runtime checks each tool call
// against that snapshot.
package permissiongate

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/adk/internal/runtimeplan"
	adktool "google.golang.org/adk/tool"
)

var (
	ErrToolDenied      = errors.New("tool is not allowed by runtime plan")
	ErrApprovalPending = errors.New("tool approval is required by runtime plan")
)

type Gate struct {
	tools map[string]runtimeplan.ToolBinding
}

type Decision struct {
	Allowed          bool
	ApprovalRequired bool
	Tool             runtimeplan.ToolBinding
	Reason           string
}

func New(plan *runtimeplan.RuntimePlan) *Gate {
	g := &Gate{tools: map[string]runtimeplan.ToolBinding{}}
	if plan == nil {
		return g
	}
	for _, item := range plan.Tools {
		name := strings.TrimSpace(item.Name)
		if name != "" {
			g.tools[name] = item
			for _, alias := range []string{
				item.RuntimeName,
				stringFromMap(item.Runtime, "name"),
				stringFromMap(item.Execution, "name"),
				mcpAlias(item),
			} {
				if alias = strings.TrimSpace(alias); alias != "" {
					g.tools[alias] = item
				}
			}
		}
	}
	return g
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil || values[key] == nil {
		return ""
	}
	return fmt.Sprint(values[key])
}

func mcpAlias(binding runtimeplan.ToolBinding) string {
	if !strings.EqualFold(binding.RuntimeType, "mcp") {
		return ""
	}
	server := stringFromMap(binding.Runtime, "server")
	name := firstNonEmpty(binding.RuntimeName, stringFromMap(binding.Runtime, "name"))
	if server == "" || name == "" {
		return ""
	}
	return server + "__" + name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (g *Gate) Check(toolName string) Decision {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return Decision{Reason: "tool name is empty"}
	}
	if g == nil || g.tools == nil {
		return Decision{Reason: "permission gate is not initialized"}
	}
	binding, ok := g.tools[name]
	if !ok {
		return Decision{Reason: "tool is absent from authorized snapshot"}
	}
	if strings.EqualFold(binding.Status, "disabled") {
		return Decision{Tool: binding, Reason: "tool snapshot status is disabled"}
	}
	if strings.TrimSpace(binding.ApprovalMode) == "" {
		return Decision{Tool: binding, Reason: "tool snapshot has no authorization mode"}
	}
	if strings.EqualFold(binding.ApprovalMode, "per_run") && !binding.Approved {
		return Decision{Tool: binding, ApprovalRequired: true, Reason: "per-run approval is required"}
	}
	return Decision{Allowed: true, Tool: binding}
}

func (g *Gate) BeforeToolCallback(ctx adktool.Context, tool adktool.Tool, args map[string]any) (map[string]any, error) {
	if tool == nil {
		return nil, fmt.Errorf("%w: nil tool", ErrToolDenied)
	}
	decision := g.Check(tool.Name())
	if decision.Allowed {
		return nil, nil
	}
	if decision.ApprovalRequired {
		if ctx != nil {
			if err := ctx.RequestConfirmation("Approve tool call "+tool.Name()+" for this Agent run.", map[string]any{
				"tool":         tool.Name(),
				"approvalMode": decision.Tool.ApprovalMode,
				"capabilities": decision.Tool.Capabilities,
				"permissions":  decision.Tool.Permissions,
			}); err != nil {
				return nil, err
			}
		}
		return map[string]any{
			"ok": false, "error": ErrApprovalPending.Error(),
			"tool": tool.Name(), "approvalRequired": true,
		}, nil
	}
	return nil, fmt.Errorf("%w: %s: %s", ErrToolDenied, tool.Name(), decision.Reason)
}
