package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/blackboard"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// SwarmContextTool exposes the swarm shared-context blackboard to agents:
// read the coordinator-curated context.md, list recent member notes, and post
// notes to the local member's shard (broadcast to members when the swarm
// layer is wired).
type SwarmContextTool struct {
	// workspace is the synced workspace root (cfg.WorkspacePath()); swarm
	// blackboards live under workspace/swarm/<id>/.
	workspace string
}

// NewSwarmContextTool creates the tool bound to the synced workspace root.
func NewSwarmContextTool(workspace string) *SwarmContextTool {
	return &SwarmContextTool{workspace: workspace}
}

func (t *SwarmContextTool) Name() string { return "swarm_context" }

func (t *SwarmContextTool) Description() string {
	return "Read and post to a swarm's shared context (blackboard). " +
		"Actions: 'read' shows the curated context document and recent notes; " +
		"'list' shows recent member notes; 'post' appends a note visible to all " +
		"swarm members (use kind/key to structure it); 'set_context' replaces " +
		"the curated context document (coordinator only)."
}

func (t *SwarmContextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"swarm_id": map[string]any{
				"type":        "string",
				"description": "Swarm identifier",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"read", "list", "post", "set_context"},
				"description": "Operation to perform",
			},
			"kind": map[string]any{
				"type":        "string",
				"description": "Note kind for 'post' (e.g. note, decision, blocker, result)",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Optional short key for 'post' notes",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Note text for 'post', or the full markdown document for 'set_context'",
			},
			"ttl_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional note expiry in seconds for 'post' (0 = never)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max notes returned by 'list' (default 50)",
			},
		},
		"required": []string{"swarm_id", "action"},
	}
}

// swarmIDPattern mirrors swarm.ValidSwarmID; kept local so this package does
// not pull the libp2p-backed swarm package into the tool surface.
var swarmIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

func (t *SwarmContextTool) board(swarmID string) (*blackboard.Blackboard, error) {
	swarmID = strings.TrimSpace(swarmID)
	if !swarmIDPattern.MatchString(swarmID) {
		return nil, fmt.Errorf("invalid swarm id %q", swarmID)
	}
	if t.workspace == "" {
		return nil, fmt.Errorf("no workspace configured")
	}
	return blackboard.New(filepath.Join(t.workspace, "swarm", swarmID), blackboard.Options{}), nil
}

func (t *SwarmContextTool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	swarmID, _ := args["swarm_id"].(string)
	action, _ := args["action"].(string)

	bb, err := t.board(swarmID)
	if err != nil {
		return toolshared.ErrorResult(err.Error())
	}
	hooks := currentSwarmHooks()

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "read":
		var sb strings.Builder
		if doc := strings.TrimSpace(bb.ReadContext()); doc != "" {
			sb.WriteString("## Curated context\n\n")
			sb.WriteString(doc)
			sb.WriteString("\n\n")
		}
		sb.WriteString(renderNotes(bb.Notes(time.Time{}), 20))
		out := strings.TrimSpace(sb.String())
		if out == "" {
			out = "The swarm blackboard is empty."
		}
		return toolshared.SilentResult(out)

	case "list":
		limit := 50
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		return toolshared.SilentResult(renderNotes(bb.Notes(time.Time{}), limit))

	case "post":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return toolshared.ErrorResult("content is required for action 'post'")
		}
		kind, _ := args["kind"].(string)
		key, _ := args["key"].(string)
		var ttl time.Duration
		if v, ok := args["ttl_seconds"].(float64); ok && v > 0 {
			ttl = time.Duration(v) * time.Second
		}
		if hooks != nil && hooks.PostNote != nil {
			if hooks.IsMember != nil && !hooks.IsMember(swarmID) {
				return toolshared.ErrorResult("not a member of swarm " + swarmID)
			}
			if err := hooks.PostNote(ctx, swarmID, kind, key, content, ttl); err != nil {
				return toolshared.ErrorResult(err.Error())
			}
			return toolshared.SilentResult("Note posted to swarm " + swarmID + " and broadcast to members.")
		}
		// No swarm layer: write the local shard directly so the note still
		// lands in the synced workspace.
		author := "local"
		if hooks != nil && hooks.PeerID != nil && hooks.PeerID() != "" {
			author = hooks.PeerID()
		}
		if err := bb.Append(author, blackboard.Note{Kind: kind, Key: key, Content: content}); err != nil {
			return toolshared.ErrorResult(err.Error())
		}
		return toolshared.SilentResult(
			"Note recorded locally in swarm " + swarmID + " (no swarm layer running; it propagates on next sync).",
		)

	case "set_context":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return toolshared.ErrorResult("content is required for action 'set_context'")
		}
		if hooks == nil || hooks.SetContext == nil {
			return toolshared.ErrorResult("set_context requires a running swarm layer")
		}
		if err := hooks.SetContext(swarmID, content); err != nil {
			return toolshared.ErrorResult(err.Error())
		}
		return toolshared.SilentResult("Curated context updated for swarm " + swarmID + ".")

	default:
		return toolshared.ErrorResult("unknown action: expected read, list, post, or set_context")
	}
}

func renderNotes(notes []blackboard.Note, limit int) string {
	if len(notes) == 0 {
		return "No notes recorded."
	}
	if limit > 0 && len(notes) > limit {
		notes = notes[len(notes)-limit:]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d note(s), newest last:\n", len(notes))
	for _, n := range notes {
		fmt.Fprintf(&sb, "- [%s] %s", n.TS.Format("2006-01-02 15:04"), n.Author)
		if n.Kind != "" {
			fmt.Fprintf(&sb, " (%s)", n.Kind)
		}
		if n.Key != "" {
			fmt.Fprintf(&sb, " %s:", n.Key)
		}
		fmt.Fprintf(&sb, " %s\n", n.Content)
	}
	return sb.String()
}
