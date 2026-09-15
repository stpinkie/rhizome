package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/stpinkie/rhizome/pkg/routing"
)

// ACPInvoker runs a prompt on an agent bound to an external ACP (Agent
// Client Protocol) process. Implemented by pkg/acp's client manager; the
// indirection keeps pkg/tools free of the ACP SDK dependency.
type ACPInvoker interface {
	RunAgent(ctx context.Context, agentID, prompt string) (string, error)
}

// ACPRunTool invokes a configured external ACP agent id directly. It is the
// escape hatch for reaching an ACP-bound agent without going through
// delegate/subagent allowlist semantics.
type ACPRunTool struct {
	invoker ACPInvoker
}

func NewACPRunTool(invoker ACPInvoker) *ACPRunTool {
	return &ACPRunTool{invoker: invoker}
}

func (t *ACPRunTool) Name() string {
	return "acp_run"
}

func (t *ACPRunTool) Description() string {
	return "Run a task on an external agent connected via the Agent Client Protocol (acp-bound agent id in agents.list). Returns the agent's final text output."
}

func (t *ACPRunTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{
				"type":        "string",
				"description": "The ACP-bound agent id to invoke",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "The task or prompt for the external agent",
			},
		},
		"required": []string{"agent_id", "task"},
	}
}

func (t *ACPRunTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.invoker == nil {
		return ErrorResult("acp_run is not configured").WithError(fmt.Errorf("no ACP invoker"))
	}
	rawAgentID, _ := args["agent_id"].(string)
	if strings.TrimSpace(rawAgentID) == "" {
		return ErrorResult("agent_id is required and must be a non-empty string")
	}
	agentID := routing.NormalizeAgentID(rawAgentID)

	task, _ := args["task"].(string)
	if strings.TrimSpace(task) == "" {
		return ErrorResult("task is required and must be a non-empty string")
	}

	out, err := t.invoker.RunAgent(ctx, agentID, task)
	if err != nil {
		return ErrorResult(fmt.Sprintf("acp agent %q failed: %v", agentID, err)).WithError(err)
	}

	return &ToolResult{
		ForLLM:  fmt.Sprintf("[Response from ACP agent %q]\n%s", agentID, out),
		ForUser: out,
	}
}
