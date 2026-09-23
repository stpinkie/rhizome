package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// NewActivityCommand prints the daemon's in-memory mesh/swarm activity feed.
func NewActivityCommand() *cobra.Command {
	var tail int
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show recent mesh/swarm activity (daemon required)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			path := "/network/activity"
			if tail > 0 {
				path += fmt.Sprintf("?tail=%d", tail)
			}
			body, status, err := internal.DaemonRequest(http.MethodGet, path, nil, 10*time.Second)
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(body))
				return
			}
			if asJSON {
				fmt.Fprintln(w, string(body))
				return
			}
			var resp struct {
				Events []mesh.ActivityEntry `json:"events"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if len(resp.Events) == 0 {
				fmt.Fprintln(w, "No recent activity.")
				return
			}
			for _, e := range resp.Events {
				fmt.Fprintf(w, "%s  %s", e.Time.Local().Format("15:04:05"), e.Kind)
				if e.Source != "" {
					fmt.Fprintf(w, "  [%s]", e.Source)
				}
				if pid, ok := e.Attrs["peer_id"].(string); ok && pid != "" {
					fmt.Fprintf(w, "  peer=%s", pid)
				}
				fmt.Fprintln(w)
			}
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 50, "maximum number of recent events to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

// NewPeerCommand prints the live detail view for one connected peer
// (conns, score, capabilities, recent tasks) — daemon required.
func NewPeerCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "peer <peer-id>",
		Short: "Show live detail for a connected mesh peer (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			want := args[0]

			body, status, err := internal.DaemonRequest(
				http.MethodGet, "/network/status", nil, 10*time.Second)
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(body))
				return
			}
			var ns mesh.NetworkStatus
			if err := json.Unmarshal(body, &ns); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			var found *mesh.PeerStatus
			for i := range ns.Peers {
				if ns.Peers[i].PeerID == want {
					found = &ns.Peers[i]
					break
				}
			}
			if found == nil {
				fmt.Fprintf(w, "peer %s is not connected\n", want)
				return
			}
			if asJSON {
				raw, _ := json.MarshalIndent(found, "", "  ")
				fmt.Fprintln(w, string(raw))
				return
			}
			printPeerDetail(w, found)
			printPeerTasks(w, want)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func printPeerDetail(w io.Writer, p *mesh.PeerStatus) {
	fmt.Fprintf(w, "Peer %s\n", p.PeerID)
	if p.Trusted {
		fmt.Fprintln(w, "  trusted: yes")
	}
	if p.Capability.Role != "" {
		fmt.Fprintf(w, "  role: %s\n", p.Capability.Role)
	}
	if p.LatencyMs > 0 {
		fmt.Fprintf(w, "  latency: %.1f ms\n", p.LatencyMs)
	}
	if p.Score != nil {
		fmt.Fprintf(w, "  score: %.0f (%d ok / %d failed, avg %.1f ms)\n",
			p.Score.Score, p.Score.Successes, p.Score.Failures, p.Score.AvgLatencyMs)
		if p.Score.LastError != "" {
			fmt.Fprintf(w, "    last error: %s\n", p.Score.LastError)
		}
	}
	if p.LastSeen != "" {
		fmt.Fprintf(w, "  last seen: %s\n", p.LastSeen)
	}
	for _, c := range p.Conns {
		fmt.Fprintf(w, "  conn: %s %s (%s, %d streams", c.Direction, c.Transport,
			shortAddr(c.RemoteMultiaddr), c.StreamCount)
		if c.OpenedAt != "" {
			fmt.Fprintf(w, ", since %s", c.OpenedAt)
		}
		fmt.Fprintln(w, ")")
	}
	if p.Bandwidth != nil {
		fmt.Fprintf(w, "  bandwidth: in %s (%.0f B/s) out %s (%.0f B/s)\n",
			humanBytes(p.Bandwidth.TotalIn), p.Bandwidth.RateIn,
			humanBytes(p.Bandwidth.TotalOut), p.Bandwidth.RateOut)
	}
	if len(p.Capability.Agents) > 0 {
		fmt.Fprintf(w, "  agents: %v\n", p.Capability.Agents)
	}
	if len(p.Capability.Models) > 0 {
		fmt.Fprintf(w, "  models: %v\n", p.Capability.Models)
	}
	if len(p.Capability.Skills) > 0 {
		fmt.Fprintf(w, "  skills: %v\n", p.Capability.Skills)
	}
	for agentID, fp := range p.Capability.AgentManifests {
		fmt.Fprintf(w, "  manifest %s: %s\n", agentID, fp)
	}
}

func printPeerTasks(w io.Writer, peerID string) {
	body, status, err := internal.DaemonRequest(
		http.MethodGet, "/network/tasks?peer="+peerID, nil, 10*time.Second)
	if err != nil || status != http.StatusOK {
		return
	}
	var resp struct {
		Tasks []agenttask.TaskInfo `json:"tasks"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp.Tasks) == 0 {
		return
	}
	fmt.Fprintln(w, "  recent tasks:")
	shown := 0
	for i := len(resp.Tasks) - 1; i >= 0 && shown < 5; i-- {
		t := resp.Tasks[i]
		line := fmt.Sprintf("    %s  %s", t.TaskID, t.Status)
		if t.AgentID != "" {
			line += fmt.Sprintf("  agent=%s", t.AgentID)
		}
		if t.Error != "" {
			line += fmt.Sprintf("  error=%s", t.Error)
		}
		if t.Usage != nil {
			line += fmt.Sprintf("  usage=%dtok/%dcalls", t.Usage.TotalTokens, t.Usage.LLMCalls)
		}
		fmt.Fprintln(w, line)
		shown++
	}
}

func shortAddr(a string) string {
	if len(a) > 48 {
		return a[:45] + "…"
	}
	return a
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
