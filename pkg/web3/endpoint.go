// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/modules"
	"github.com/stpinkie/rhizome/pkg/utils"
)

// EndpointSource identifies where the resolved endpoint came from. Tool
// output reports the source label, not raw URLs (a URL may embed an API
// key in its path).
type EndpointSource string

const (
	SourceOverride    EndpointSource = "tools.web3.endpoint"
	SourceEthereumRPC EndpointSource = "ethereum-rpc module"
	SourceNimbus      EndpointSource = "nimbus-verified-proxy module"
)

// Endpoint is a resolved JSON-RPC target.
type Endpoint struct {
	URL    string
	APIKey string
	Source EndpointSource
}

// resolveTTL is how long a successful resolution (endpoint + chain check)
// is cached. Failures are never cached — the next call retries.
const resolveTTL = 30 * time.Second

// Provider resolves the configured Ethereum endpoint and dispatches calls.
// Safe for concurrent use; one Provider is shared across agents.
type Provider struct {
	cfg    *config.Config
	hc     *http.Client
	hcErr  error
	static *Endpoint // set by NewStaticProvider — skips resolution
	probe  func(ctx context.Context, url string) error

	mu         sync.Mutex
	resolved   *Endpoint
	resolvedAt time.Time
}

// NewProvider builds a resolver bound to cfg. The HTTP client is built via
// utils.CreateSafeHTTPClient so tools.web3.allow_private_endpoints=false
// wires the safe-dial SSRF guard (private/restricted IPs and DNS-rebinding
// blocked at dial time).
func NewProvider(cfg *config.Config) *Provider {
	p := &Provider{cfg: cfg}
	timeout := 30 * time.Second
	allowPrivate := func() bool { return false }
	if cfg != nil {
		timeout = cfg.Tools.Web3.GetTimeout()
		allowPrivate = func() bool { return cfg.Tools.Web3.AllowPrivateEndpoints }
	}
	hc, err := utils.CreateSafeHTTPClient(utils.SafeHTTPClientOptions{
		Timeout:           timeout,
		AllowPrivateHosts: allowPrivate,
	})
	p.hc, p.hcErr = hc, err
	p.probe = p.defaultProbe
	return p
}

// NewStaticProvider returns a Provider pinned to one endpoint — for tests
// and local development. Skips resolution, the chain check, and the
// safe-dial guard.
func NewStaticProvider(endpoint, apiKey string) *Provider {
	return &Provider{
		static: &Endpoint{URL: endpoint, APIKey: apiKey, Source: SourceOverride},
		hc:     &http.Client{Timeout: 30 * time.Second},
	}
}

// defaultProbe verifies a module endpoint answers eth_chainId.
func (p *Provider) defaultProbe(ctx context.Context, rawURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := NewClient(rawURL, "", p.hc).Call(ctx, "eth_chainId", nil)
	return err
}

// Resolve returns the effective endpoint, honoring the 30 s cache.
// Resolution and the chain check run outside the mutex — concurrent
// callers may duplicate a probe on cache miss, which is harmless; the
// mutex only guards the resolved/resolvedAt cache fields.
func (p *Provider) Resolve(ctx context.Context) (*Endpoint, error) {
	if p.hcErr != nil {
		return nil, fmt.Errorf("web3: HTTP client setup: %w", p.hcErr)
	}
	if p.static != nil {
		return p.static, nil
	}
	p.mu.Lock()
	if p.resolved != nil && time.Since(p.resolvedAt) < resolveTTL {
		ep := p.resolved
		p.mu.Unlock()
		return ep, nil
	}
	p.mu.Unlock()

	ep, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.checkChain(ctx, ep); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.resolved, p.resolvedAt = ep, time.Now()
	p.mu.Unlock()
	return ep, nil
}

// Source returns the label of the last resolved endpoint, or "" before the
// first successful resolution.
func (p *Provider) Source() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.static != nil {
		return string(p.static.Source)
	}
	if p.resolved == nil {
		return ""
	}
	return string(p.resolved.Source)
}

// resolve applies the documented precedence:
// tools.web3.endpoint → ethereum-rpc module endpoint_url (+secret api_key)
// → probed nimbus-verified-proxy listen_url → descriptive error.
func (p *Provider) resolve(ctx context.Context) (*Endpoint, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("web3: no config available")
	}
	w3 := p.cfg.Tools.Web3

	if raw := strings.TrimSpace(w3.Endpoint); raw != "" {
		if err := p.validateEndpoint(ctx, raw); err != nil {
			return nil, fmt.Errorf("tools.web3.endpoint: %w", err)
		}
		return &Endpoint{URL: raw, APIKey: w3.APIKey.String(), Source: SourceOverride}, nil
	}

	if mc, ok := p.cfg.Modules["ethereum-rpc"]; ok {
		if raw := strings.TrimSpace(mc.Fields["endpoint_url"]); raw != "" {
			if err := p.validateEndpoint(ctx, raw); err != nil {
				return nil, fmt.Errorf("modules.ethereum-rpc endpoint_url: %w", err)
			}
			apiKey := ""
			if s, ok := mc.Secrets["api_key"]; ok {
				apiKey = s.String()
			}
			return &Endpoint{
				URL:    raw,
				APIKey: apiKey,
				Source: SourceEthereumRPC,
			}, nil
		}
	}

	var nimbusErr error
	if spec, ok := modules.Lookup("nimbus-verified-proxy"); ok {
		listen := ""
		if f, ok := spec.Field("listen_url"); ok {
			listen = f.Default
		}
		if mc, ok := p.cfg.Modules["nimbus-verified-proxy"]; ok {
			if v := strings.TrimSpace(mc.Fields["listen_url"]); v != "" {
				listen = v
			}
		}
		if listen != "" {
			if err := p.validateEndpoint(ctx, listen); err != nil {
				nimbusErr = err
			} else if err := p.probe(ctx, listen); err != nil {
				nimbusErr = fmt.Errorf("nimbus-verified-proxy not answering: %w", err)
			} else {
				return &Endpoint{URL: listen, Source: SourceNimbus}, nil
			}
		}
	}

	msg := "web3: no endpoint available — set tools.web3.endpoint, configure the " +
		"ethereum-rpc module (endpoint_url), or run nimbus-verified-proxy " +
		"(rhizome module install nimbus-verified-proxy; module enable; daemon start)"
	if nimbusErr != nil {
		msg += fmt.Sprintf(" (nimbus candidate rejected: %v)", nimbusErr)
	}
	return nil, fmt.Errorf("%s", msg)
}

// checkChain enforces tools.web3.chain_ids against the endpoint's
// eth_chainId. Empty list = any chain.
func (p *Provider) checkChain(ctx context.Context, ep *Endpoint) error {
	w3 := p.cfg.Tools.Web3
	if len(w3.ChainIDs) == 0 {
		return nil
	}
	raw, err := NewClient(ep.URL, ep.APIKey, p.hc).Call(ctx, "eth_chainId", nil)
	if err != nil {
		return fmt.Errorf("web3: chain check failed against %s: %w", ep.Source, err)
	}
	var hexID string
	if err := json.Unmarshal(raw, &hexID); err != nil {
		return fmt.Errorf("web3: invalid eth_chainId response from %s", ep.Source)
	}
	id, err := QuantityUint64(hexID)
	if err != nil {
		return fmt.Errorf("web3: invalid chain id %q from %s", hexID, ep.Source)
	}
	for _, allowed := range w3.ChainIDs {
		if id == allowed {
			return nil
		}
	}
	return fmt.Errorf(
		"web3: endpoint %s is on chain %d, which is not in tools.web3.chain_ids %v",
		ep.Source, id, w3.ChainIDs)
}

// validateEndpoint enforces http/https + non-empty host, and rejects
// literal private hosts early when allow_private_endpoints=false (the
// dial-time guard additionally covers DNS-rebinding and redirects).
func (p *Provider) validateEndpoint(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https endpoint URLs are allowed (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host in endpoint URL")
	}
	allow := true
	if p.cfg != nil {
		allow = p.cfg.Tools.Web3.AllowPrivateEndpoints
	}
	if !allow {
		host := u.Hostname()
		if utils.IsObviousPrivateHost(host, nil, nil) {
			return fmt.Errorf(
				"endpoint host %q is private/loopback — set tools.web3.allow_private_endpoints=true to permit it",
				host)
		}
		if ip := net.ParseIP(host); ip == nil {
			// Resolve hostnames so a public-looking name cannot point at a
			// private or restricted address.
			addrs, err := lookupIPAddr(ctx, host)
			if err != nil {
				return fmt.Errorf("could not resolve endpoint host %q: %w", host, err)
			}
			for _, a := range addrs {
				if utils.IsPrivateOrRestrictedIP(a.IP) {
					return fmt.Errorf(
						"endpoint host %q resolves to private/restricted address %s",
						host, a.IP)
				}
			}
		} else if utils.IsPrivateOrRestrictedIP(ip) {
			return fmt.Errorf(
				"endpoint host %q is a private/restricted address", host)
		}
	}
	return nil
}

// lookupIPAddr resolves endpoint hostnames for the SSRF check. A var so
// tests can stub DNS without a live resolver.
var lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// Call resolves the endpoint (cached) and performs the JSON-RPC call.
func (p *Provider) Call(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if p.hcErr != nil {
		return nil, fmt.Errorf("web3: HTTP client setup: %w", p.hcErr)
	}
	ep, err := p.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	return NewClient(ep.URL, ep.APIKey, p.hc).Call(ctx, method, params)
}
