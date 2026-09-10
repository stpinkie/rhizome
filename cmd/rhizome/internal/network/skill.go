package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// skillRequest issues a daemon request to /network/skills*.
func skillRequest(method, path string, body []byte, timeout time.Duration) ([]byte, int, error) {
	base, token := pairDaemonURL()
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

// NewSkillCommand implements `rhizome mesh skill list|pull`.
func NewSkillCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Share and pull skills between trusted mesh peers",
		Long: "Trusted peers advertise shareable skills via the " +
			"mesh.skill_share allowlist; `skill pull` fetches a signed, " +
			"hash-verified bundle and installs it under ~/.rhizome/skills " +
			"with mesh:<peer> origin metadata.",
	}

	list := &cobra.Command{
		Use:   "list <peer-id>",
		Short: "List a peer's shareable skills (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			data, code, err := skillRequest(http.MethodGet,
				"/network/skills?peer="+args[0], nil, 30*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — mesh skill commands require a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", string(bytes.TrimSpace(data)))
				os.Exit(1)
			}
			if asJSON {
				fmt.Println(string(data))
				return
			}
			var res struct {
				Skills []string `json:"skills"`
			}
			_ = json.Unmarshal(data, &res)
			if len(res.Skills) == 0 {
				fmt.Printf("Peer %s shares no skills.\n", args[0])
				return
			}
			fmt.Printf("Shareable skills on %s:\n", args[0])
			for _, s := range res.Skills {
				fmt.Printf("  - %s\n", s)
			}
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")

	var allowSuspicious bool

	pull := &cobra.Command{
		Use:   "pull <peer-id> <skill-name>",
		Short: "Pull a skill bundle from a trusted peer (daemon required)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			payload, _ := json.Marshal(map[string]any{
				"peer":             args[0],
				"name":             args[1],
				"allow_suspicious": allowSuspicious,
			})
			data, code, err := skillRequest(http.MethodPost,
				"/network/skills/pull", payload, 2*time.Minute)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — mesh skill commands require a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", string(bytes.TrimSpace(data)))
				os.Exit(1)
			}
			if asJSON {
				fmt.Println(string(data))
				return
			}
			var res struct {
				Name       string   `json:"name"`
				Dir        string   `json:"dir"`
				Suspicious bool     `json:"suspicious"`
				Matches    []string `json:"matches"`
			}
			_ = json.Unmarshal(data, &res)
			fmt.Printf("Pulled skill %q from %s\nInstalled to %s\n", res.Name, args[0], res.Dir)
			if res.Suspicious {
				fmt.Println("Warning: guard flagged suspicious content:")
				for _, m := range res.Matches {
					fmt.Printf("  - %s\n", m)
				}
			}
		},
	}
	pull.Flags().BoolVar(&allowSuspicious, "allow-suspicious", false, "Install bundles even if the guard scanner flags them as suspicious")

	cmd.AddCommand(list, pull)
	return cmd
}
