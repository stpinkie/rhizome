package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// NewActivityCommand prints the daemon's mesh/swarm activity feed. The feed
// is backed by mesh-activity.jsonl when mesh.activity_log is on, so --since
// reads back across restarts.
func NewActivityCommand() *cobra.Command {
	var tail int
	var asJSON bool
	var kind, peerID, swarmID, since string

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show recent mesh/swarm activity (daemon required)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			vals := url.Values{}
			if tail > 0 {
				vals.Set("tail", fmt.Sprintf("%d", tail))
			}
			if kind != "" {
				vals.Set("kind", kind)
			}
			if peerID != "" {
				vals.Set("peer", peerID)
			}
			if swarmID != "" {
				vals.Set("swarm", swarmID)
			}
			if since != "" {
				vals.Set("since", since)
			}
			path := "/network/activity"
			if len(vals) > 0 {
				path += "?" + vals.Encode()
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
	cmd.Flags().IntVar(&tail, "tail", 50, "maximum number of matching events to show")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by event kind (exact, or prefix ending in '.*' like mesh.*)")
	cmd.Flags().StringVar(&peerID, "peer", "", "filter to events mentioning this peer id")
	cmd.Flags().StringVar(&swarmID, "swarm", "", "filter to events for this swarm id")
	cmd.Flags().StringVar(&since, "since", "", "only events newer than this (duration like 30m, or RFC3339)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

// NewTraceCommand correlates a task id across the task store, activity feed,
// and audit trail (daemon required).
func NewTraceCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "trace <task-id>",
		Short: "Correlate a task id across task store, activity, and audit (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			path := "/network/trace?task=" + url.QueryEscape(args[0])
			body, status, err := internal.DaemonRequest(http.MethodGet, path, nil, 15*time.Second)
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
			PrintTraceReport(w, body)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

// PrintTraceReport renders the JSON body of GET /network/trace in a compact
// human-readable form. Shared by `mesh trace` and `swarm trace`.
func PrintTraceReport(w io.Writer, body []byte) {
	var rep struct {
		ID string `json:"id"`
		// MeshTaskSnapshot marshals its Go field names (the type has no
		// json tags), so the mirror uses the capitalized keys.
		Task *struct {
			ID      string `json:"ID"`
			CorrID  string `json:"CorrID"`
			Owner   string `json:"Owner"`
			AgentID string `json:"AgentID"`
			Status  string `json:"Status"`
			Err     string `json:"Err"`
		} `json:"task"`
		Offer *struct {
			SwarmID  string `json:"swarm_id"`
			Status   string `json:"status"`
			Assignee string `json:"assignee,omitempty"`
			TaskID   string `json:"task_id,omitempty"`
		} `json:"offer"`
		Run *struct {
			RunID     string    `json:"run_id"`
			SwarmID   string    `json:"swarm_id"`
			Goal      string    `json:"goal"`
			Status    string    `json:"status"`
			StartedAt time.Time `json:"started_at"`
		} `json:"run"`
		Events []mesh.ActivityEntry `json:"events"`
		Audit  []json.RawMessage    `json:"audit"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return
	}
	fmt.Fprintf(w, "Trace %s\n", rep.ID)
	if rep.Task != nil {
		t := rep.Task
		line := fmt.Sprintf("  task: %s  %s", t.ID, t.Status)
		if t.AgentID != "" {
			line += fmt.Sprintf("  agent=%s", t.AgentID)
		}
		if t.Err != "" {
			line += fmt.Sprintf("  error=%s", t.Err)
		}
		fmt.Fprintln(w, line)
		fmt.Fprintf(w, "    owner=%s  corr=%s\n", t.Owner, t.CorrID)
	}
	if rep.Offer != nil {
		o := rep.Offer
		line := fmt.Sprintf("  offer: swarm=%s  status=%s", o.SwarmID, o.Status)
		if o.Assignee != "" {
			line += fmt.Sprintf("  assignee=%s", o.Assignee)
		}
		if o.TaskID != "" {
			line += fmt.Sprintf("  task=%s", o.TaskID)
		}
		fmt.Fprintln(w, line)
	}
	if rep.Run != nil {
		r := rep.Run
		fmt.Fprintf(w, "  run: %s  swarm=%s  status=%s  started=%s\n",
			r.RunID, r.SwarmID, r.Status, r.StartedAt.Local().Format("2006-01-02 15:04:05"))
		if r.Goal != "" {
			fmt.Fprintf(w, "    goal=%s\n", r.Goal)
		}
	}
	if len(rep.Events) > 0 {
		fmt.Fprintln(w, "  events:")
		for _, e := range rep.Events {
			fmt.Fprintf(w, "    %s  %s", e.Time.Local().Format("15:04:05"), e.Kind)
			if e.Source != "" {
				fmt.Fprintf(w, "  [%s]", e.Source)
			}
			fmt.Fprintln(w)
		}
	}
	if len(rep.Audit) > 0 {
		fmt.Fprintln(w, "  audit:")
		for _, raw := range rep.Audit {
			var a map[string]any
			if json.Unmarshal(raw, &a) != nil {
				continue
			}
			ts, _ := a["ts"].(string)
			op, _ := a["op"].(string)
			peer, _ := a["peer_id"].(string)
			stat, _ := a["status"].(string)
			fmt.Fprintf(w, "    %s  %s  %s  %s\n", ts, op, peer, stat)
		}
	}
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
		if p.Score.Decay != nil && *p.Score.Decay < 1 {
			fmt.Fprintf(w, "    evidence decayed to %.0f%% (idle since last seen)\n", *p.Score.Decay*100)
		}
		if p.Score.LastError != "" {
			fmt.Fprintf(w, "    last error: %s\n", p.Score.LastError)
		}
		if len(p.Score.Ops) > 0 {
			ops := make([]string, 0, len(p.Score.Ops))
			for op := range p.Score.Ops {
				ops = append(ops, op)
			}
			sort.Strings(ops)
			for _, op := range ops {
				st := p.Score.Ops[op]
				line := fmt.Sprintf("    %s: %d ok / %d failed", op, st.Successes, st.Failures)
				if st.AvgLatency > 0 {
					line += fmt.Sprintf(" (avg %.1f ms)", float64(st.AvgLatency)/float64(time.Millisecond))
				}
				fmt.Fprintln(w, line)
			}
		}
		if len(p.Score.Outcomes) > 0 {
			fmt.Fprintln(w, "    market outcomes:")
			for _, o := range p.Score.Outcomes {
				line := fmt.Sprintf("      %s %s", o.Op, o.Outcome)
				if o.SessionID != "" {
					line += fmt.Sprintf(" session=%s", o.SessionID)
				}
				if o.ValueHash != "" {
					vh := o.ValueHash
					if len(vh) > 12 {
						vh = vh[:12]
					}
					line += fmt.Sprintf(" value=%s", vh)
				}
				if o.At > 0 {
					line += fmt.Sprintf(" at %s", time.Unix(o.At, 0).UTC().Format(time.RFC3339))
				}
				fmt.Fprintln(w, line)
			}
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
