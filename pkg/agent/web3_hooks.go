// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package agent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	web3tools "github.com/stpinkie/rhizome/pkg/tools/web3"
)

// web3ConfigPath resolves the active config.json path — RHIZOME_CONFIG
// first, then <RHIZOME_HOME>/config.json (mirrors cmd internal helpers).
func web3ConfigPath() string {
	if p := os.Getenv(config.EnvConfig); p != "" {
		return p
	}
	return filepath.Join(config.GetHome(), "config.json")
}

// web3ApprovalHook adapts HookManager.ApproveTool into the signing tools'
// normalized veto pass. The pipeline already vets every tool call by name +
// raw args; this inner pass sees the resolved request (signer, chain,
// selector, human summary) before a pending entry is queued.
type web3ApprovalHook struct {
	hm *HookManager
}

func (h web3ApprovalHook) ApproveWeb3Action(
	ctx context.Context, action *web3tools.Web3ApprovalAction,
) (bool, string) {
	if h.hm == nil || action == nil {
		return true, ""
	}
	d := h.hm.ApproveTool(ctx, &ToolApprovalRequest{
		Meta: HookMeta{Source: "web3.approval"},
		Tool: action.Tool,
		Arguments: map[string]any{
			"kind":      action.Kind,
			"summary":   action.Summary,
			"from":      action.From,
			"to":        action.To,
			"value_wei": action.ValueWei,
			"chain_id":  action.ChainID,
			"selector":  action.Selector,
		},
	})
	return d.Approved, d.Reason
}

// web3Emit returns an emit func publishing web3.* runtime events on the
// agent loop's bus (nil-safe when the loop/bus is absent).
func web3Emit(al *AgentLoop) func(kind string, attrs map[string]any) {
	var bus runtimeevents.Bus
	if al != nil {
		bus = al.RuntimeEventBus()
	}
	return func(kind string, attrs map[string]any) {
		if bus == nil {
			return
		}
		bus.PublishNonBlocking(runtimeevents.Event{
			Kind:     runtimeevents.Kind(kind),
			Source:   runtimeevents.Source{Component: "web3"},
			Severity: runtimeevents.SeverityInfo,
			Attrs:    attrs,
		})
	}
}
