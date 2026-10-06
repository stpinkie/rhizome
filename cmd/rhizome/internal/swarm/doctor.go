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

// doctorReport mirrors the daemon's swarm.Doctor response.
type doctorReport struct {
	SwarmID       string   `json:"swarm_id"`
	SelfID        string   `json:"self_id"`
	Coordinator   string   `json:"coordinator"`
	CoordinatorUp bool     `json:"coordinator_up"`
	Queried       int      `json:"queried"`
	Reachable     int      `json:"reachable"`
	Asymmetric    []string `json:"asymmetric"`
	Undiscovered  []string `json:"undiscovered"`
	Members       []struct {
		PeerID    string `json:"peer_id"`
		Reachable bool   `json:"reachable"`
		ListsUs   bool   `json:"lists_us"`
		Epoch     int64  `json:"epoch"`
		Error     string `json:"error"`
	} `json:"members"`
}

func newDoctorCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor <swarm-id>",
		Short: "Query every known member and diff rosters (daemon required)",
		Long: "Asks each known roster member (bounded by max_members, 5s each) " +
			"which swarms it joined and which members it knows, then reports " +
			"coordinator reachability, members that do not list us back " +
			"(asymmetric), and members others know that we do not " +
			"(undiscovered).",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			swarmID := args[0]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			// Worst case: max_members(32) × 5s query timeout + slack.
			data, code, err := daemonRequest(http.MethodGet,
				"/network/swarms/"+swarmID+"/doctor", nil, 3*time.Minute)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — doctor requires a running daemon\n", err)
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
					fmt.Println(string(out))
					return
				}
				fmt.Println(string(data))
				return
			}
			var rep doctorReport
			if err := json.Unmarshal(data, &rep); err != nil {
				fmt.Println(string(data))
				return
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Swarm %s — coordinator %s", rep.SwarmID, shortPeer(rep.Coordinator))
			if rep.Coordinator == "" {
				fmt.Fprint(w, "(none)")
			} else if rep.CoordinatorUp {
				fmt.Fprint(w, " (reachable)")
			} else {
				fmt.Fprint(w, " (UNREACHABLE)")
			}
			fmt.Fprintln(w)
			fmt.Fprintf(w, "Members: %d queried, %d reachable\n", rep.Queried, rep.Reachable)
			for _, m := range rep.Members {
				switch {
				case !m.Reachable:
					err := m.Error
					if err == "" {
						err = "no response"
					}
					fmt.Fprintf(w, "  - %s  unreachable (%s)\n", shortPeer(m.PeerID), err)
				case !m.ListsUs:
					fmt.Fprintf(w, "  - %s  reachable, does NOT list us", shortPeer(m.PeerID))
					if m.Epoch > 0 {
						fmt.Fprintf(w, " (epoch %d)", m.Epoch)
					}
					fmt.Fprintln(w)
				default:
					fmt.Fprintf(w, "  - %s  ok", shortPeer(m.PeerID))
					if m.Epoch > 0 {
						fmt.Fprintf(w, " (epoch %d)", m.Epoch)
					}
					fmt.Fprintln(w)
				}
			}
			if len(rep.Asymmetric) > 0 {
				fmt.Fprintf(w, "Asymmetric (joined but don't list us): %v\n", rep.Asymmetric)
			}
			if len(rep.Undiscovered) > 0 {
				fmt.Fprintf(w, "Undiscovered (listed by others, not in our roster): %v\n", rep.Undiscovered)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print raw JSON report")
	return cmd
}

// shortPeer trims a peer id for table output.
func shortPeer(id string) string {
	if len(id) > 16 {
		return id[:8] + "…" + id[len(id)-6:]
	}
	return id
}
