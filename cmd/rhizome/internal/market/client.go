// Package market implements the rhizome market command tree — thin verbs
// that call the rhizome-market companion module's loopback HTTP API. The
// module publishes its listener address in <module_dir>/api.addr and the
// daemon mints its bearer token at <module_dir>/bridge-token; without the
// module the verbs print the not-installed posture.
package market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/modules"
)

const (
	// marketModuleID is the first-party market companion module's catalog id.
	marketModuleID = "rhizome-market"
	// apiAddrFile is where the module publishes its loopback HTTP listener.
	apiAddrFile = "api.addr"
	// bridgeTokenFile is the per-module bearer minted by the daemon; the
	// module reuses it to authenticate its loopback API.
	bridgeTokenFile = "bridge-token"

	apiAddrMaxBytes   = 4 << 10
	bridgeTokMaxBytes = 1 << 10
	apiTimeout        = 15 * time.Second
)

// apiClient is a resolved handle on the module's loopback API.
type apiClient struct {
	addr  string // "host:port", loopback-validated
	token string
	hc    *http.Client
}

// errNotInstalled reports the rhizome-market-missing posture verbatim.
var errNotInstalled = errors.New(
	"rhizome-market module not installed — run `rhizome module install rhizome-market`")

// moduleManager builds a read-side module manager (daemonless-safe).
func moduleManager() (*modules.Manager, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, err
	}
	return modules.NewManager(internal.GetRhizomeHome(), cfg, nil, nil), nil
}

// resolveClient locates the module dir and reads api.addr + bridge-token.
// Error strings are user-facing posture messages.
func resolveClient() (*apiClient, error) {
	mgr, err := moduleManager()
	if err != nil {
		return nil, err
	}
	dir := mgr.Dir(marketModuleID)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, errNotInstalled
	}
	addrRaw, err := readBounded(filepath.Join(dir, apiAddrFile), apiAddrMaxBytes)
	if err != nil {
		return nil, fmt.Errorf(
			"rhizome-market module installed but its API isn't up — check `rhizome module status %s`",
			marketModuleID)
	}
	addr := strings.TrimSpace(addrRaw)
	if addr == "" {
		return nil, fmt.Errorf(
			"rhizome-market module api.addr is empty — check `rhizome module status %s`",
			marketModuleID)
	}
	if err := requireLoopback(addr); err != nil {
		return nil, err
	}
	tokRaw, err := readBounded(filepath.Join(dir, bridgeTokenFile), bridgeTokMaxBytes)
	if err != nil || strings.TrimSpace(tokRaw) == "" {
		return nil, fmt.Errorf(
			"rhizome-market module bridge token missing — reinstall or restart the module")
	}
	return &apiClient{
		addr:  addr,
		token: strings.TrimSpace(tokRaw),
		hc:    &http.Client{Timeout: apiTimeout},
	}, nil
}

// requireLoopback refuses non-loopback api.addr values: the module API is a
// localhost surface, and a tampered file must not leak the bearer off-host.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("rhizome-market module api.addr %q is malformed: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf(
		"rhizome-market module api.addr %q is not a loopback address — refusing", addr)
}

// call POSTs body to /v1/<verb> and returns the response body verbatim.
func (c *apiClient) call(ctx context.Context, verb string, body any) ([]byte, int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "http://"+c.addr+"/v1/"+verb, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf(
			"rhizome-market module API unreachable at %s — module may be stopped; check `rhizome module status %s`",
			c.addr, marketModuleID)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, advertBodyMaxBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// advertBodyMaxBytes bounds one API response read (1 MiB is generous for
// find/buy/session/receipt payloads).
const advertBodyMaxBytes = 1 << 20

func readBounded(path string, max int64) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is under the module dir.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > max {
		return "", fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), max)
	}
	return string(data), nil
}
