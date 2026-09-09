package network

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

// pairDaemonURL returns the running daemon's gateway base URL and bearer
// token from the pid file, or ("", "") when no daemon is running.
func pairDaemonURL() (base, token string) {
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

func pairRequest(path string, body []byte, timeout time.Duration) ([]byte, int, error) {
	base, token := pairDaemonURL()
	if base == "" {
		return nil, 0, fmt.Errorf("no running daemon found")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, base+path, reader)
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

// NewPairCommand implements `rhizome network pair` — create or redeem a
// trust-pairing bundle on the running daemon.
func NewPairCommand() *cobra.Command {
	var create bool
	var accept string
	var ttl time.Duration
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair with another node (share bundle → mutual trust)",
		Long: "Trust pairing exchanges peer ids and addrs over a signed, " +
			"single-use code instead of manual peer-id copying. " +
			"`--create` prints a bundle to share; `--accept <bundle>` redeems it.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if create == (accept != "") {
				fmt.Fprintln(os.Stderr, "Error: pass exactly one of --create or --accept <bundle>")
				os.Exit(1)
			}
			if create {
				payload, _ := json.Marshal(map[string]string{"ttl": ttl.String()})
				data, code, err := pairRequest("/network/pair", payload, 15*time.Second)
				if err != nil {
					fmt.Fprintf(os.Stderr,
						"Error: %v — pairing requires a running daemon with mesh.enabled\n", err)
					os.Exit(1)
				}
				if code != http.StatusOK {
					fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
					os.Exit(1)
				}
				if asJSON {
					fmt.Println(string(data))
					return
				}
				var resp struct {
					Bundle string `json:"bundle"`
					TTL    string `json:"ttl"`
				}
				_ = json.Unmarshal(data, &resp)
				fmt.Printf("Pairing bundle (single-use, valid %s):\n\n%s\n\n", resp.TTL, resp.Bundle)
				fmt.Println("Share it out of band; on the other node run:")
				fmt.Println("  rhizome network pair --accept <bundle>")
				return
			}

			payload, _ := json.Marshal(map[string]string{"bundle": accept})
			data, code, err := pairRequest("/network/pair/accept", payload, 60*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr,
					"Error: %v — pairing requires a running daemon with mesh.enabled\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			if asJSON {
				fmt.Println(string(data))
				return
			}
			var resp struct {
				PeerID string `json:"peer_id"`
			}
			_ = json.Unmarshal(data, &resp)
			fmt.Printf("Paired with %s — mutual trust persisted.\n", resp.PeerID)
		},
	}
	cmd.Flags().BoolVar(&create, "create", false, "Mint a single-use pairing bundle")
	cmd.Flags().StringVar(&accept, "accept", "", "Redeem a pairing bundle")
	cmd.Flags().DurationVar(&ttl, "ttl", 15*time.Minute, "Pairing code lifetime (with --create)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}
