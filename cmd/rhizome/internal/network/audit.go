package network

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// NewAuditCommand returns the audit command, which prints the tail of the
// local mesh audit trail (~/.rhizome/mesh-audit.jsonl).
func NewAuditCommand() *cobra.Command {
	var tail int
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Show the tail of the mesh audit log",
		Long: "Print recent entries from the local mesh audit trail " +
			"(mesh-audit.jsonl under RHIZOME_HOME). The daemon appends one " +
			"JSONL entry per remote operation when mesh.audit_log is enabled.",
		Run: func(cmd *cobra.Command, _ []string) {
			path := filepath.Join(config.GetHome(), "mesh-audit.jsonl")
			entries, err := mesh.ReadAuditTail(path, tail)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading audit log: %v\n", err)
				os.Exit(1)
			}

			if asJSON {
				out, err := json.MarshalIndent(entries, "", "  ")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error encoding audit entries: %v\n", err)
					os.Exit(1)
				}
				cmd.Println(string(out))
				return
			}

			if len(entries) == 0 {
				cmd.Printf("No audit entries in %s\n", path)
				return
			}

			for _, raw := range entries {
				var e struct {
					TS         string `json:"ts"`
					PeerID     string `json:"peer_id"`
					Op         string `json:"op"`
					AgentID    string `json:"agent_id"`
					Ref        string `json:"ref"`
					Status     string `json:"status"`
					DurationMs int64  `json:"duration_ms"`
					Detail     string `json:"detail"`
				}
				if err := json.Unmarshal(raw, &e); err != nil {
					continue
				}
				line := fmt.Sprintf("%s  %-8s %-8s", e.TS, e.Op, e.Status)
				if e.PeerID != "" {
					line += fmt.Sprintf("  peer=%s", e.PeerID)
				}
				if e.AgentID != "" {
					line += fmt.Sprintf("  agent=%s", e.AgentID)
				}
				if e.Ref != "" {
					line += fmt.Sprintf("  ref=%s", e.Ref)
				}
				line += fmt.Sprintf("  %dms", e.DurationMs)
				if e.Detail != "" {
					line += fmt.Sprintf("  detail=%s", e.Detail)
				}
				cmd.Println(line)
			}
		},
	}

	cmd.Flags().IntVar(&tail, "tail", 20, "Number of recent entries to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print raw JSON entries")
	return cmd
}
