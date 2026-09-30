// Package evolution implements `rhizome evolution` — the operator surface
// for the self-evolution subsystem (v0.18.0 Track 149). Reads are daemonless
// (they load the workspace's evolution state dir directly); mutations
// (run, draft accept/reject) go through the daemon's /evolution/* API.
package evolution

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

// NewEvolutionCommand builds the `rhizome evolution` verb tree.
func NewEvolutionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evolution",
		Short: "Inspect and control agent self-evolution",
		Long: "Inspect and control the self-evolution loop: learning records, " +
			"generated skill drafts, apply/rollback state. Read verbs work " +
			"without a running daemon; run and draft accept/reject require it.",
	}
	cmd.AddCommand(
		newStatusCommand(),
		newRunCommand(),
		newDraftsCommand(),
		newDraftCommand(),
		newRecordsCommand(),
		newSkillsCommand(),
	)
	return cmd
}

type workspaceSelection struct {
	workspace string
	agent     string
}

func (s *workspaceSelection) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&s.workspace, "workspace", "", "workspace path override")
	cmd.Flags().StringVar(&s.agent, "agent", "", "agent id whose configured workspace to use")
}

// workspaces resolves the selection to concrete workspace paths. With neither
// flag it returns every workspace evolution state can live under; with
// --workspace or --agent it returns exactly one.
func (s workspaceSelection) workspaces(cfg *config.Config) ([]string, error) {
	if s.workspace != "" {
		return []string{expandPath(s.workspace)}, nil
	}
	if s.agent != "" {
		ws, ok := rhevo.AgentWorkspace(cfg, s.agent)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", s.agent)
		}
		return []string{ws}, nil
	}
	return rhevo.ConfigWorkspaces(cfg), nil
}

// singleWorkspace is the mutation form: one workspace, defaulting to the
// configured default when no flag is given.
func (s workspaceSelection) singleWorkspace(cfg *config.Config) (string, error) {
	list, err := s.workspaces(cfg)
	if err != nil {
		return "", err
	}
	if s.workspace == "" && s.agent == "" {
		return cfg.WorkspacePath(), nil
	}
	if len(list) == 0 {
		return "", fmt.Errorf("no workspace resolved")
	}
	return list[0], nil
}

func loadEvolutionConfig() (*config.Config, error) {
	return internal.LoadConfig()
}

func storeFor(cfg *config.Config, workspace string) *rhevo.Store {
	var stateDir string
	if cfg != nil {
		stateDir = cfg.Evolution.StateDir
	}
	return rhevo.NewStore(rhevo.NewPaths(workspace, stateDir))
}

func expandPath(path string) string {
	return rhevo.ResolveWorkspacePath(path)
}

// findDraftAcross locates a draft by id (prefix match allowed) across the
// resolved workspaces, requiring exactly one match.
func findDraftAcross(cfg *config.Config, s workspaceSelection, draftID string) (rhevo.SkillDraft, string, error) {
	workspaces, err := s.workspaces(cfg)
	if err != nil {
		return rhevo.SkillDraft{}, "", err
	}
	var (
		found    *rhevo.SkillDraft
		foundWS  string
		searched int
	)
	for _, ws := range workspaces {
		drafts, err := rhevo.SortedDrafts(storeFor(cfg, ws), "")
		if err != nil {
			continue
		}
		searched++
		for _, d := range drafts {
			if d.ID != draftID && !strings.HasPrefix(d.ID, draftID) {
				continue
			}
			// A shared state_dir surfaces the same draft from every
			// workspace's store — only a *different* draft id matching
			// the prefix is ambiguous.
			if found != nil && found.ID != d.ID {
				return rhevo.SkillDraft{}, "", fmt.Errorf(
					"draft id %q is ambiguous across workspaces; pass --workspace", draftID)
			}
			if found == nil {
				m := d
				found = &m
				foundWS = d.WorkspaceID
				if foundWS == "" {
					foundWS = ws
				}
			}
		}
	}
	if found == nil {
		return rhevo.SkillDraft{}, "", fmt.Errorf(
			"draft %q not found (searched %d workspace stores)",
			draftID,
			searched,
		)
	}
	return *found, foundWS, nil
}
