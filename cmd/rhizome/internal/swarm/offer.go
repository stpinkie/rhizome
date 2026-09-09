package swarm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/pid"
)

// daemonBaseURL returns the running daemon's gateway base URL and bearer
// token from the pid file, or ("", "") when no daemon is running.
func daemonBaseURL() (base, token string) {
	data := pid.ReadPidFileWithCheck(internal.GetRhizomeHome())
	if data == nil || data.Port == 0 || data.Token == "" {
		return "", ""
	}
	host := data.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(data.Port))), data.Token
}

// daemonRequest performs an authenticated request against the daemon's
// gateway and returns the response body.
func daemonRequest(method, path string, body []byte, timeout time.Duration) ([]byte, int, error) {
	base, token := daemonBaseURL()
	if base == "" {
		return nil, 0, fmt.Errorf("no running daemon found")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func newOfferCommand() *cobra.Command {
	var model string
	var tools []string
	var attaches []string
	var asJSON bool
	var wait bool
	var waitDur time.Duration
	var cancelOfferID string
	var reqAgents, reqModels, reqSkills []string

	cmd := &cobra.Command{
		Use:   "offer <swarm-id> <agent-id> <task>",
		Short: "Offer a task to a swarm's work queue (daemon required)",
		Args: func(cmd *cobra.Command, args []string) error {
			if cancelOfferID != "" {
				return cobra.ExactArgs(1)(cmd, args)
			}
			return cobra.ExactArgs(3)(cmd, args)
		},
		Run: func(cmd *cobra.Command, args []string) {
			swarmID := args[0]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			// Cancel mode: withdraw an open or assigned offer.
			if cancelOfferID != "" {
				payload, _ := json.Marshal(map[string]string{"offer_id": cancelOfferID})
				data, code, err := daemonRequest(http.MethodPost,
					"/network/swarms/"+swarmID+"/offers/cancel", payload, 15*time.Second)
				if err != nil {
					fmt.Fprintf(
						os.Stderr,
						"Error: %v — cancelling requires a running daemon (rhizome daemon)\n",
						err,
					)
					os.Exit(1)
				}
				if code != http.StatusOK {
					fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
					os.Exit(1)
				}
				fmt.Printf("Offer %s cancelled in swarm %q\n", cancelOfferID, swarmID)
				return
			}

			agentID, err := internal.ValidateAgentID(args[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			task := args[2]

			// Attachment paths are resolved on the daemon host (the CLI talks
			// to the local daemon, so they share a filesystem).
			var media []map[string]string
			for _, p := range attaches {
				if strings.TrimSpace(p) != "" {
					media = append(media, map[string]string{"path": p})
				}
			}
			var requires map[string]any
			if len(reqAgents) > 0 || len(reqModels) > 0 || len(reqSkills) > 0 {
				requires = map[string]any{
					"agents": reqAgents,
					"models": reqModels,
					"skills": reqSkills,
				}
			}
			payload, _ := json.Marshal(map[string]any{
				"agent_id": agentID,
				"model":    model,
				"task":     task,
				"tools":    tools,
				"media":    media,
				"requires": requires,
			})
			data, code, err := daemonRequest(http.MethodPost,
				"/network/swarms/"+swarmID+"/offers", payload, 15*time.Second)
			if err != nil {
				fmt.Fprintf(
					os.Stderr,
					"Error: %v — the swarm offer queue requires a running daemon (rhizome daemon)\n",
					err,
				)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}

			var offerResp struct {
				OfferID string `json:"offer_id"`
			}
			_ = json.Unmarshal(data, &offerResp)
			if offerResp.OfferID == "" {
				fmt.Println(string(data))
				return
			}

			if !wait {
				if asJSON {
					fmt.Println(string(data))
				} else {
					fmt.Printf("Offer %s published to swarm %q — claims resolve in the background\n",
						offerResp.OfferID, swarmID)
					fmt.Printf("Track with: rhizome swarm offers %s\n", swarmID)
				}
				return
			}

			// Poll the offers list until this offer leaves "open".
			deadline := time.Now().Add(waitDur)
			for time.Now().Before(deadline) {
				list, lcode, lerr := daemonRequest(http.MethodGet,
					"/network/swarms/"+swarmID+"/offers", nil, 10*time.Second)
				if lerr != nil || lcode != http.StatusOK {
					break
				}
				var parsed struct {
					Offers []map[string]any `json:"offers"`
				}
				_ = json.Unmarshal(list, &parsed)
				for _, o := range parsed.Offers {
					if o["offer_id"] != offerResp.OfferID {
						continue
					}
					if o["status"] != "open" {
						if asJSON {
							out, _ := json.MarshalIndent(o, "", "  ")
							fmt.Println(string(out))
						} else {
							fmt.Printf("Offer %s: %s", offerResp.OfferID, o["status"])
							if a, ok := o["assignee"].(string); ok && a != "" {
								fmt.Printf(" → %s", a)
							}
							if tid, ok := o["task_id"].(string); ok && tid != "" {
								fmt.Printf(" (task %s)", tid)
							}
							if e, ok := o["error"].(string); ok && e != "" {
								fmt.Printf(" — %s", e)
							}
							fmt.Println()
						}
						return
					}
				}
				time.Sleep(time.Second)
			}
			fmt.Printf("Offer %s still open after %s\n", offerResp.OfferID, waitDur)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Model override for the remote agent")
	cmd.Flags().StringSliceVar(&tools, "tools", nil, "Allowed tool names for the remote agent")
	cmd.Flags().
		StringArrayVar(&attaches, "attach", nil, "Attach a file to the offered task (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the offer to resolve")
	cmd.Flags().DurationVar(&waitDur, "wait-timeout", 60*time.Second, "Max wait with --wait")
	cmd.Flags().
		StringVar(&cancelOfferID, "cancel", "", "Cancel the given offer id (usage: offer <swarm-id> --cancel <id>)")
	cmd.Flags().
		StringSliceVar(&reqAgents, "require-agent", nil, "Only members hosting one of these agent ids may claim")
	cmd.Flags().
		StringSliceVar(&reqModels, "require-model", nil, "Only members advertising one of these models may claim")
	cmd.Flags().
		StringSliceVar(&reqSkills, "require-skill", nil, "Only members with one of these skills may claim")
	return cmd
}

func newOffersCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "offers <swarm-id>",
		Short: "List tracked offers for a swarm (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := internal.ValidateSwarmID(args[0]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			data, code, err := daemonRequest(http.MethodGet,
				"/network/swarms/"+args[0]+"/offers", nil, 10*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — offers require a running daemon\n", err)
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
			var parsed struct {
				Offers []struct {
					OfferID  string `json:"offer_id"`
					AgentID  string `json:"agent_id"`
					Offerer  string `json:"offerer"`
					Status   string `json:"status"`
					Assignee string `json:"assignee,omitempty"`
					TaskID   string `json:"task_id,omitempty"`
					Error    string `json:"error,omitempty"`
				} `json:"offers"`
			}
			if err := json.Unmarshal(data, &parsed); err != nil || len(parsed.Offers) == 0 {
				cmd.Printf("No tracked offers for swarm %q.\n", args[0])
				return
			}
			cmd.Printf("Offers for %q:\n", args[0])
			for _, o := range parsed.Offers {
				line := fmt.Sprintf("  - %s agent=%s status=%s", o.OfferID, o.AgentID, o.Status)
				if o.Assignee != "" {
					line += " → " + o.Assignee
				}
				if o.TaskID != "" {
					line += " task=" + o.TaskID
				}
				if o.Error != "" {
					line += " err=" + o.Error
				}
				cmd.Println(line)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}
