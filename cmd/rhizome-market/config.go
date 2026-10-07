// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/modules"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// offersMaxBytes bounds the offers.json file / offers_json field payload.
// Offers land verbatim in advert.json, which the daemon caps at 16 KiB —
// a larger offers set can never be advertised, so the source is bounded
// well under the advert budget to fail fast instead of silently omitting.
const (
	offersMaxBytes = 256 << 10
	offersMaxCount = 64
)

// offer is one sellable service entry from offers_json / offers.json. The
// shape is the design's C1 schema; price strings stay strings (exact
// decimal semantics belong to the settlement layer, not a float).
type offer struct {
	ID           string     `json:"id"`
	AgentBinding string     `json:"agent_binding"`
	PriceSheet   priceSheet `json:"price_sheet"`
}

type priceSheet struct {
	PerTask         string `json:"per_task,omitempty"`
	Per1kPrompt     string `json:"per_1k_prompt_tokens,omitempty"`
	Per1kCompletion string `json:"per_1k_completion_tokens,omitempty"`
	MinCharge       string `json:"min_charge,omitempty"`
	Asset           string `json:"asset"`
	ChainID         int64  `json:"chain_id"`
}

// marketConfig is the resolved view of modules.rhizome-market.fields +
// .secrets + catalog defaults. Errs collects non-fatal validation problems:
// the module keeps running (buy side can still work, a bad offer must not
// kill the API) but advert emission refuses to claim serving on errors.
type marketConfig struct {
	fields                 map[string]string
	serveEnabled           bool
	offers                 []offer
	runtime                string // sandbox|container
	payoutChainID          int64
	payoutAddress          string
	payoutAsset            string
	maxSessions            int
	sessionTTL             time.Duration
	buyMaxCostPerTask      string
	buyMaxCostPerDay       string
	exportAllowAttachments bool
	exportRedact           bool
	exportRequireReview    string
	indexEnabled           bool
	indexURL               string
	indexPubKey            string                 // curator Ed25519 pubkey (base64); empty = baked release key
	dhtEnabled             bool                   // market_dht — unvetted rendezvous tier; requires host dht.enabled
	buyerAddress           string                 // purchaser EVM identity; empty = web3 wallet default
	buyAutoRelease         bool                   // release() immediately after receipt verify
	signerMode             string                 // approval|direct|wallet
	allowMainnet           bool                   // escrow_allow_mainnet — chain 1 opt-in
	watchInterval          time.Duration          // escrow event scan cadence; 0 disables
	httpsListen            string                 // serve_https_listen — TLS/wss ACP endpoint (empty=off)
	httpsCert              string                 // serve_https_cert — unset = persisted self-signed
	httpsKey               string                 // serve_https_key — paired with cert
	httpsMaxConns          int                    // serve_https_max_conns — simultaneous wss streams
	httpsAdvertise         string                 // serve_https_advertise — public host[:port] or wss:// URL
	rail                   *settlement.RailConfig // nil = fixture posture
	errs                   []string
}

// loadMarketConfig resolves the module's spec from the embedded catalog,
// merges defaults ← fields ← secrets (mirroring Manager.resolvedFields),
// parses + validates every field into marketConfig. Never fails on bad
// values — problems land in errs so the module stays up and reports them.
func loadMarketConfig(cfg *config.Config, moduleDir string) *marketConfig {
	mc := &marketConfig{}
	spec, ok := moduleSpec()
	if !ok {
		mc.errs = append(mc.errs, "catalog has no rhizome-market spec")
		return mc
	}
	values := map[string]string{}
	for _, f := range spec.ConfigFields {
		if f.Default != "" {
			values[f.Key] = expandModuleDir(f.Default, moduleDir)
		}
	}
	entry := cfg.Modules[moduleID]
	for k, v := range entry.Fields {
		values[k] = v
	}
	for k, v := range entry.Secrets {
		values[k] = v.String()
	}
	mc.fields = values

	mc.serveEnabled = truthy(values["serve_enabled"])
	mc.runtime = strField(mc, values, "runtime", "sandbox")
	if mc.runtime != "sandbox" && mc.runtime != "container" {
		mc.errs = append(mc.errs, fmt.Sprintf(
			"runtime %q must be \"sandbox\" or \"container\"", mc.runtime))
	}
	mc.payoutChainID = intField(mc, values, "payout_chain_id")
	mc.payoutAddress = strField(mc, values, "payout_address", "")
	if mc.payoutAddress != "" && !web3.IsAddress(mc.payoutAddress) {
		mc.errs = append(mc.errs, "payout_address is not a valid 0x address")
	}
	mc.payoutAsset = strField(mc, values, "payout_asset", "USDC")
	mc.maxSessions = intFieldDef(mc, values, "max_concurrent_sessions", 4)
	if mc.maxSessions < 1 || mc.maxSessions > 1024 {
		mc.errs = append(mc.errs, "max_concurrent_sessions must be 1–1024")
	}
	mc.sessionTTL = durationField(mc, values, "session_ttl", 30*time.Minute)
	mc.buyMaxCostPerTask = moneyField(mc, values, "buy_max_cost_per_task")
	mc.buyMaxCostPerDay = moneyField(mc, values, "buy_max_cost_per_day")
	mc.exportAllowAttachments = truthy(values["export_allow_attachments"])
	mc.exportRedact = truthyDefault(values["export_redact"], true)
	mc.exportRequireReview = strField(mc, values, "export_require_review", "prompt")
	switch mc.exportRequireReview {
	case "prompt", "always", "never":
	default:
		mc.errs = append(mc.errs,
			"export_require_review must be prompt|always|never")
	}
	mc.indexEnabled = truthy(values["market_index_enabled"])
	mc.indexURL = strField(mc, values, "market_index_url", "")
	if mc.indexURL != "" {
		if u, err := url.Parse(mc.indexURL); err != nil ||
			(u.Scheme != "https" && !isLoopbackURL(u)) {
			mc.errs = append(mc.errs,
				"market_index_url must be https (or loopback for testing)")
		}
	}
	mc.indexPubKey = strField(mc, values, "market_index_pubkey", "")
	if mc.indexPubKey != "" {
		if raw, err := base64.StdEncoding.DecodeString(mc.indexPubKey); err != nil ||
			len(raw) != ed25519.PublicKeySize {
			mc.errs = append(mc.errs,
				"market_index_pubkey must be a base64 Ed25519 public key")
		}
	}
	// market_dht opts into the unvetted DHT rendezvous tier — providers
	// announce on rhizome-market-v1 and buyers query it under the index.
	// The bridge refuses cleanly when the host's dht.enabled is off, so an
	// enabled module on a dht-less daemon degrades to index+journal rows.
	mc.dhtEnabled = truthy(values["market_dht"])
	mc.buyerAddress = strField(mc, values, "buyer_address", "")
	if mc.buyerAddress != "" && !web3.IsAddress(mc.buyerAddress) {
		mc.errs = append(mc.errs, "buyer_address is not a valid 0x address")
	}
	mc.buyAutoRelease = truthyDefault(values["buy_auto_release"], true)
	mc.signerMode = strField(mc, values, "settlement_signer", "approval")
	switch mc.signerMode {
	case "approval", "direct", "wallet":
	default:
		mc.errs = append(mc.errs,
			"settlement_signer must be approval|direct|wallet")
	}
	mc.httpsListen = strField(mc, values, "serve_https_listen", "")
	if mc.httpsListen != "" {
		if _, _, err := net.SplitHostPort(mc.httpsListen); err != nil {
			mc.errs = append(mc.errs,
				"serve_https_listen must be host:port: "+err.Error())
			// Neutralize: an invalid listen must not reach startHTTPS —
			// the errs model keeps the module up and reports instead of
			// letting an optional listener fail the process.
			mc.httpsListen = ""
		}
	}
	mc.httpsCert = strField(mc, values, "serve_https_cert", "")
	mc.httpsKey = strField(mc, values, "serve_https_key", "")
	if (mc.httpsCert == "") != (mc.httpsKey == "") {
		mc.errs = append(mc.errs,
			"serve_https_cert and serve_https_key must be set together")
		// Neutralize — a half-pair reaching loadOrGenHTTPSCert would fail
		// the optional listener hard instead of reporting via errs.
		mc.httpsCert, mc.httpsKey = "", ""
	}
	mc.httpsMaxConns = intFieldDef(mc, values, "serve_https_max_conns", 64)
	if mc.httpsMaxConns < 1 || mc.httpsMaxConns > 4096 {
		mc.errs = append(mc.errs, "serve_https_max_conns must be 1–4096")
	}
	mc.httpsAdvertise = strField(mc, values, "serve_https_advertise", "")
	if mc.httpsAdvertise != "" {
		u := mc.httpsAdvertise
		if strings.HasPrefix(u, "https://") {
			u = "wss://" + strings.TrimPrefix(u, "https://")
		}
		if strings.Contains(u, "://") && !strings.HasPrefix(u, "wss://") {
			mc.errs = append(mc.errs,
				"serve_https_advertise must be host:port or a wss:// URL")
		} else if !strings.HasPrefix(u, "wss://") {
			if _, _, err := net.SplitHostPort(u); err != nil {
				mc.errs = append(mc.errs,
					"serve_https_advertise must be host:port or a wss:// URL")
			}
		}
	}

	mc.allowMainnet = truthy(values["escrow_allow_mainnet"])
	mc.watchInterval = durationField(
		mc, values, "escrow_watch_interval", time.Minute)
	if mc.watchInterval != 0 &&
		(mc.watchInterval < 15*time.Second || mc.watchInterval > time.Hour) {
		mc.errs = append(mc.errs,
			"escrow_watch_interval must be 0 (off) or 15s–1h")
	}

	mc.offers = loadOffers(mc, values, moduleDir)

	rail, err := settlement.ConfigFromFields(values)
	if err != nil {
		mc.errs = append(mc.errs, fmt.Sprintf("escrow config: %s", err))
	} else {
		mc.rail = rail
	}
	if mc.rail == nil && (mc.signerMode == "wallet" || mc.signerMode == "direct") {
		mc.errs = append(mc.errs,
			"settlement_signer="+mc.signerMode+
				" has no effect without escrow_contract (fixture rail signs nothing)")
	}
	return mc
}

// moduleSpec resolves the module's own catalog entry — defaults and field
// keys stay single-sourced in pkg/modules rather than duplicated here.
func moduleSpec() (modules.ModuleSpec, bool) {
	return modules.Lookup(moduleID)
}

// expandModuleDir substitutes the catalog-default {module_dir} placeholder
// (Manager.resolvedFields semantics) so a field default can point under the
// module dir.
func expandModuleDir(v, moduleDir string) string {
	return strings.ReplaceAll(v, "{module_dir}", moduleDir)
}

// loadOffers parses the offer list: <module_dir>/offers.json wins when
// present and non-empty (large offer sets stay editable without rewriting
// config); otherwise the offers_json field. A malformed source is an error,
// never a silent fallback.
func loadOffers(mc *marketConfig, values map[string]string, moduleDir string) []offer {
	path := filepath.Join(moduleDir, offersFile)
	var raw []byte
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		data, err := readBounded(path, offersMaxBytes)
		if err != nil {
			mc.errs = append(mc.errs, fmt.Sprintf("offers.json: %s", err))
			return nil
		}
		raw = data
	} else if v := strings.TrimSpace(values["offers_json"]); v != "" {
		raw = []byte(v)
	} else {
		return nil
	}
	var offers []offer
	if err := json.Unmarshal(raw, &offers); err != nil {
		mc.errs = append(mc.errs, fmt.Sprintf("offers parse: %s", err))
		return nil
	}
	if len(offers) > offersMaxCount {
		mc.errs = append(mc.errs,
			fmt.Sprintf("offers exceed %d entries (%d)", offersMaxCount, len(offers)))
		return nil
	}
	seen := map[string]bool{}
	for i, o := range offers {
		if o.ID == "" {
			mc.errs = append(mc.errs, fmt.Sprintf("offers[%d]: id required", i))
			continue
		}
		if seen[o.ID] {
			mc.errs = append(mc.errs, fmt.Sprintf("offers[%d]: duplicate id %q", i, o.ID))
			continue
		}
		seen[o.ID] = true
		if o.AgentBinding == "" {
			mc.errs = append(mc.errs,
				fmt.Sprintf("offers[%d] (%s): agent_binding required", i, o.ID))
		}
		if o.PriceSheet.Asset == "" {
			mc.errs = append(mc.errs,
				fmt.Sprintf("offers[%d] (%s): price_sheet.asset required", i, o.ID))
		}
		for _, p := range []struct{ name, v string }{
			{"per_task", o.PriceSheet.PerTask},
			{"per_1k_prompt_tokens", o.PriceSheet.Per1kPrompt},
			{"per_1k_completion_tokens", o.PriceSheet.Per1kCompletion},
			{"min_charge", o.PriceSheet.MinCharge},
		} {
			if p.v != "" && !validDecimal(p.v) {
				mc.errs = append(mc.errs,
					fmt.Sprintf("offers[%d] (%s): price_sheet.%s %q not a decimal",
						i, o.ID, p.name, p.v))
			}
		}
	}
	return offers
}

// advertable reports whether the config supports a serving claim: serving
// enabled, a valid payout address, at least one offer, and no validation
// errors. The advert must not lie about a sellable service.
func (mc *marketConfig) advertable() bool {
	return mc.serveEnabled && mc.payoutAddress != "" &&
		len(mc.offers) > 0 && len(mc.errs) == 0
}

// --- scalar helpers: record errs instead of failing ---------------------

func strField(mc *marketConfig, values map[string]string, key, def string) string {
	if v := strings.TrimSpace(values[key]); v != "" {
		return v
	}
	return def
}

func intField(mc *marketConfig, values map[string]string, key string) int64 {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		mc.errs = append(mc.errs, fmt.Sprintf("%s %q is not an integer", key, v))
		return 0
	}
	return n
}

func intFieldDef(mc *marketConfig, values map[string]string, key string, def int) int {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		mc.errs = append(mc.errs, fmt.Sprintf("%s %q is not an integer", key, v))
		return def
	}
	return n
}

func durationField(
	mc *marketConfig, values map[string]string, key string, def time.Duration,
) time.Duration {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		mc.errs = append(mc.errs,
			fmt.Sprintf("%s %q is not a positive duration", key, v))
		return def
	}
	return d
}

// moneyField validates a decimal string (kept as the string — pricing
// precision belongs to settlement, not float64).
func moneyField(mc *marketConfig, values map[string]string, key string) string {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return ""
	}
	if !validDecimal(v) {
		mc.errs = append(mc.errs, fmt.Sprintf("%s %q is not a decimal", key, v))
		return ""
	}
	return v
}

// validDecimal accepts non-negative decimal amounts ("0.05", "12", "1e-3");
// big.Rat parses exactly and rejects junk.
func validDecimal(s string) bool {
	r, ok := new(big.Rat).SetString(s)
	return ok && r.Sign() >= 0
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func truthyDefault(v string, def bool) bool {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return truthy(v)
}

func isLoopbackURL(u *url.URL) bool {
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// readBounded reads a file capped at limit+1 and reports overage.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: under the module dir.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), limit)
	}
	return data, nil
}
