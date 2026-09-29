package evolution

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
)

// ConfigWorkspaces returns every workspace evolution state can live under:
// the defaults workspace plus each configured agent's workspace override.
// Order is deterministic (defaults first, then sorted agent workspaces).
func ConfigWorkspaces(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Agents.List)+1)
	seen := make(map[string]struct{}, len(cfg.Agents.List)+1)
	add := func(ws string) {
		ws = expandEvolutionHome(strings.TrimSpace(ws))
		if ws == "" {
			return
		}
		if _, ok := seen[ws]; ok {
			return
		}
		seen[ws] = struct{}{}
		out = append(out, ws)
	}
	add(cfg.WorkspacePath())
	var extra []string
	for _, a := range cfg.Agents.List {
		if w := expandEvolutionHome(strings.TrimSpace(a.Workspace)); w != "" {
			if _, ok := seen[w]; !ok {
				extra = append(extra, w)
			}
		}
	}
	sort.Strings(extra)
	for _, w := range extra {
		add(w)
	}
	return out
}

// AgentWorkspace resolves an agent id to its configured workspace, falling
// back to the defaults workspace when the agent has no override. The bool
// reports whether the agent id exists in config.
func AgentWorkspace(cfg *config.Config, agentID string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	for _, a := range cfg.Agents.List {
		if a.ID != agentID {
			continue
		}
		if w := expandEvolutionHome(strings.TrimSpace(a.Workspace)); w != "" {
			return w, true
		}
		return cfg.WorkspacePath(), true
	}
	return "", false
}

// ResolveWorkspacePath normalizes an operator-supplied workspace path —
// trim + ~ expansion — so CLI flags and API params resolve identically.
func ResolveWorkspacePath(path string) string {
	return expandEvolutionHome(strings.TrimSpace(path))
}

// expandEvolutionHome mirrors config.expandHome for paths resolved outside
// config load (CLI/daemonless reads share it).
func expandEvolutionHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
