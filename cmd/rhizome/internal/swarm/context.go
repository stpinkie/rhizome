package swarm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
)

// newContextCommand prints a swarm's shared context: the coordinator-curated
// document plus recent member notes.
func newContextCommand() *cobra.Command {
	var asJSON bool
	var since string
	var setFile string

	cmd := &cobra.Command{
		Use:   "context <swarm-id>",
		Short: "Show a swarm's shared context (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			swarmID := args[0]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			// --set writes the curated context document (coordinator only).
			if setFile != "" {
				content, err := os.ReadFile(setFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				payload, _ := json.Marshal(map[string]any{
					"action":  "set_context",
					"content": string(content),
				})
				data, code, err := daemonRequest(http.MethodPost,
					"/network/swarms/"+swarmID+"/context", payload, 15*time.Second)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v — requires a running daemon\n", err)
					os.Exit(1)
				}
				if code != http.StatusOK {
					fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
					os.Exit(1)
				}
				fmt.Fprintf(w, "Curated context updated for swarm %q\n", swarmID)
				return
			}

			path := "/network/swarms/" + swarmID + "/context"
			if since != "" {
				path += "?since=" + since
			}
			data, code, err := daemonRequest(http.MethodGet, path, nil, 15*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — requires a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			if asJSON {
				var pretty any
				if json.Unmarshal(data, &pretty) == nil {
					out, _ := json.MarshalIndent(pretty, "", "  ")
					fmt.Fprintln(w, string(out))
					return
				}
				fmt.Fprintln(w, string(data))
				return
			}

			var parsed struct {
				Context string `json:"context"`
				Notes   []struct {
					TS      time.Time `json:"ts"`
					Author  string    `json:"author"`
					Kind    string    `json:"kind,omitempty"`
					Key     string    `json:"key,omitempty"`
					Content string    `json:"content"`
				} `json:"notes"`
			}
			if err := json.Unmarshal(data, &parsed); err != nil {
				fmt.Fprintln(w, string(data))
				return
			}
			if strings.TrimSpace(parsed.Context) != "" {
				fmt.Fprintf(w, "== Curated context ==\n%s\n\n", parsed.Context)
			}
			if len(parsed.Notes) == 0 {
				fmt.Fprintln(w, "No shared notes.")
				return
			}
			fmt.Fprintln(w, "== Notes (newest last) ==")
			for _, n := range parsed.Notes {
				line := fmt.Sprintf("  [%s] %s", n.TS.Format("2006-01-02 15:04"), n.Author)
				if n.Kind != "" {
					line += " (" + n.Kind + ")"
				}
				if n.Key != "" {
					line += " " + n.Key + ":"
				}
				fmt.Fprintln(w, line+" "+n.Content)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	cmd.Flags().StringVar(&since, "since", "", "Only notes newer than this RFC3339 timestamp")
	cmd.Flags().StringVar(&setFile, "set", "", "Replace curated context.md with this file's contents (coordinator only)")
	return cmd
}

// newNoteCommand appends a note to the local member's blackboard shard and
// broadcasts it to swarm members.
func newNoteCommand() *cobra.Command {
	var kind, key string
	var ttl time.Duration

	cmd := &cobra.Command{
		Use:   "note <swarm-id> <content>",
		Short: "Post a note to a swarm's shared context (daemon required)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			swarmID := args[0]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			payload, _ := json.Marshal(map[string]any{
				"action":      "note",
				"kind":        kind,
				"key":         key,
				"content":     args[1],
				"ttl_seconds": ttl.Seconds(),
			})
			data, code, err := daemonRequest(http.MethodPost,
				"/network/swarms/"+swarmID+"/context", payload, 15*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — requires a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			fmt.Fprintf(w, "Note posted to swarm %q\n", swarmID)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "note", "Note kind (note, decision, blocker, result, ...)")
	cmd.Flags().StringVar(&key, "key", "", "Optional short key")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "Note expiry (e.g. 24h); 0 keeps it forever")
	return cmd
}
