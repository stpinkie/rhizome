package network

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/blob"
)

// NewBlobsCommand implements `rhizome mesh blobs` — a daemonless listing of
// the local blob store (~/.rhizome/blobs) with TTL state.
func NewBlobsCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "blobs",
		Short: "List the local blob store with TTL state",
		Long: "List content-addressed blobs under <RHIZOME_HOME>/blobs: hash, " +
			"name, size, owner, stored/expires times, and in-flight partials " +
			"staged for resumable transfers. Works without a running daemon.",
		Run: func(cmd *cobra.Command, _ []string) {
			meshCfg := config.DefaultMeshConfig()
			if cfg, err := config.LoadConfig(internal.GetConfigPath()); err == nil && cfg != nil {
				meshCfg = cfg.Mesh
			}
			store := blob.NewStore(
				filepath.Join(config.GetHome(), "blobs"),
				meshCfg.BlobMaxBytes, meshCfg.BlobTTL)
			entries, err := store.List()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading blob store: %v\n", err)
				os.Exit(1)
			}

			if asJSON {
				out, err := json.MarshalIndent(entries, "", "  ")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error encoding blob entries: %v\n", err)
					os.Exit(1)
				}
				cmd.Println(string(out))
				return
			}

			if len(entries) == 0 {
				cmd.Println("No blobs stored.")
				return
			}
			for _, e := range entries {
				line := fmt.Sprintf("%s…  %10d bytes", e.Hash[:16], e.Size)
				if e.Partial > 0 {
					line = fmt.Sprintf("%s…  %10d staged (partial)", e.Hash[:16], e.Partial)
				}
				if e.Name != "" {
					line += fmt.Sprintf("  %s", e.Name)
				}
				if e.Owner != "" {
					line += fmt.Sprintf("  from %s", shortPID(e.Owner))
				}
				if e.StoredAt > 0 {
					line += fmt.Sprintf("  stored %s", time.Unix(e.StoredAt, 0).Format("2006-01-02 15:04"))
				}
				if e.ExpiresAt > 0 {
					line += fmt.Sprintf("  expires %s", time.Unix(e.ExpiresAt, 0).Format("2006-01-02 15:04"))
				}
				cmd.Println(line)
			}
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "Print raw JSON entries")
	return cmd
}

func shortPID(pid string) string {
	if len(pid) > 18 {
		return pid[:8] + "…" + pid[len(pid)-6:]
	}
	return pid
}
