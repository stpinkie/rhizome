//go:build economy

package modules

import "slices"

// marketCatalogEntry is the rhizome-market companion-module spec. It is
// bucket-B (ALIGNMENT.md §3): excluded from the default catalog and only
// appended under the `economy` build tag, preserving its original slot
// between ipfs-kubo and llama-cpp.
var marketCatalogEntry = ModuleSpec{
	ID: "rhizome-market", Name: "Rhizome Market",
	Kind:    KindDaemon,
	License: "MIT",
	Description: "Open-agent work market: sell-side serving under an " +
		"isolated runtime + buy-side purchasing with escrow settlement. " +
		"First-party module — shares RHIZOME_HOME identity and wallet; " +
		"install is a real trust decision mitigated by digest-pinned " +
		"delivery.",
	// First-party: ships as rhizome-market_{goos}_{goarch} assets on the
	// same repo release. Platforms are narrower than the base binary —
	// the container serving runtime is the constraint.
	Platforms: []string{
		"linux/amd64", "linux/arm64",
		"darwin/amd64", "darwin/arm64",
		"windows/amd64", "windows/arm64",
	},
	Install: InstallSpec{
		Method:        "github-release",
		Repo:          "stpinkie/rhizome",
		TagTemplate:   "v{version}",
		AssetTemplate: "rhizome-market_{goos}_{goarch}.tar.gz",
		AssetTemplates: map[string]string{
			"windows": "rhizome-market_{goos}_{goarch}.zip",
		},
		Binary: "rhizome-market",
		// Chicken-and-egg: pinned digests need released assets, so the
		// entry lands uninstallable (releases:[]) — the first pin is
		// added in Track 107 or emitted by the release pipeline.
		Releases: []ReleasePin{},
	},
	// The module self-reads modules.rhizome-market.fields/.secrets via
	// the config loader — no Arg/Env mappings, so secrets never touch
	// argv or the process environment.
	Run:       RunSpec{Workdir: "."},
	Protocols: []string{"/rhizome/acp/1.0.0"},
	// peer_score reports settled-session outcomes to the mesh's
	// PeerScoreStore — market reputation never touches the file itself.
	// dht_provide/dht_find are the unvetted rendezvous tier — the
	// daemon prefix-locks namespaces to rhizome-market-*.
	BridgeActions: []string{"peer_score", "dht_provide", "dht_find"},
	ConfigFields: []ConfigField{
		{
			Key:     "serve_enabled",
			Label:   "Advertise sell-side serving in the mesh manifest",
			Default: "false",
		},
		{
			Key:   "offers_json",
			Label: "Offer list JSON [{id, agent_binding, price_sheet{…}}]; overridden by <module_dir>/offers.json",
		},
		{Key: "runtime", Label: "Serving runtime (sandbox|container)", Default: "sandbox"},
		{Key: "payout_chain_id", Label: "Payout chain id (e.g. 8453 Base, 11155111 Sepolia)"},
		{Key: "payout_address", Label: "Payout address (0x… — public; lands in advert.json)"},
		{Key: "payout_asset", Label: "Payout asset symbol", Default: "USDC"},
		{
			Key:     "max_concurrent_sessions",
			Label:   "Max simultaneous market sessions",
			Default: "4",
		},
		{Key: "session_ttl", Label: "Session idle TTL (e.g. 30m)", Default: "30m"},
		{Key: "buy_max_cost_per_task", Label: "Buy-side per-task spend cap"},
		{Key: "buy_max_cost_per_day", Label: "Buy-side daily spend cap"},
		{
			Key:     "export_allow_attachments",
			Label:   "Allow attachments in outbound tasks",
			Default: "false",
		},
		{Key: "export_redact", Label: "Run pkg/redact on outbound tasks", Default: "true"},
		{
			Key:     "export_require_review",
			Label:   "Review before send (prompt|always|never)",
			Default: "prompt",
		},
		{
			Key:     "market_index_enabled",
			Label:   "Query a curated provider index",
			Default: "false",
		},
		{
			Key:   "market_dht",
			Label: "Unvetted DHT rendezvous tier (rhizome-market-v1; needs host dht.enabled)",
		},
		{Key: "market_index_url", Label: "Provider index URL (https://…)", Secret: true},
		{
			Key:   "market_index_pubkey",
			Label: "Index curator Ed25519 pubkey (base64; unset = release key)",
		},
		{Key: "buyer_address", Label: "Buyer EVM address (unset = default web3 wallet)"},
		{
			Key:     "buy_auto_release",
			Label:   "Auto-release escrow on verified receipt",
			Default: "true",
		},
		{
			Key:     "settlement_signer",
			Label:   "Tx signing mode (approval|direct|wallet — wallet signs autonomously)",
			Default: "approval",
		},
		{
			Key:     "escrow_allow_mainnet",
			Label:   "Permit escrow on chain 1 (default refuse)",
			Default: "false",
		},
		{
			Key:     "escrow_watch_interval",
			Label:   "Escrow event scan interval (0=off)",
			Default: "60s",
		},
		{
			Key:   "serve_https_listen",
			Label: "TLS listen addr for direct wss buyers (host:port; empty=off)",
		},
		{
			Key:   "serve_https_cert",
			Label: "TLS cert PEM path (unset = persisted self-signed pair)",
		},
		{
			Key:   "serve_https_key",
			Label: "TLS key PEM path (must pair with serve_https_cert)",
		},
		{Key: "serve_https_max_conns", Label: "Max simultaneous wss streams", Default: "64"},
		{
			Key:   "serve_https_advertise",
			Label: "Public endpoint for adverts (host:port or wss://…; unset = bound listen addr)",
		},
		{Key: "escrow_chain_id", Label: "Escrow chain id (11155111 Sepolia)"},
		{
			Key:   "escrow_rail",
			Label: "Escrow rail (smart_invoice|rhizome — graduated session-keyed contract)",
		},
		{
			Key:   "escrow_contract",
			Label: "Escrow contract address (Smart Invoice factory or RhizomeEscrow; unset = fixture rail)",
		},
		{Key: "escrow_token", Label: "Escrow ERC-20 token address"},
		{
			Key:   "escrow_arbiter",
			Label: "Escrow arbiter (0x address | kleros:<court> — court id for ERC-792 escalation)",
		},
		{
			Key:   "escrow_arbiter_adapter",
			Label: "RhizomeKlerosAdapter contract (required when escrow_arbiter=kleros:*)",
		},
		{Key: "escrow_dispute_window", Label: "Dispute window in seconds"},
		{
			Key:     "escrow_settlement",
			Label:   "Settlement mode (per_task|drawdown — drawdown needs escrow_rail=rhizome)",
			Default: "per_task",
		},
		{Key: "session_budget", Label: "Drawdown session budget (decimal token units)"},
		{Key: "draw_interval", Label: "Min seconds between on-chain draws (0=unpaced)"},
		{
			Key:   "tee_kind",
			Label: "TEE posture claim (tdx|sev-snp|trustzone|sgx|other|none; unset = platform probe)",
		},
		{
			Key:   "tee_report_url",
			Label: "TEE report endpoint (https URL serving fresh quotes)",
		},
		{
			Key:   "tee_evidence_hash",
			Label: "TEE evidence commitment (sha256 hex, optional 0x)",
		},
	},
	Notes: "Loopback API for `rhizome market` (api.addr + bridge-token " +
		"auth). Claims /rhizome/acp/1.0.0 — mutually exclusive with " +
		"acp.server.remote: the bridge claims the protocol for whichever " +
		"registers first (daemon skips the module claim while core " +
		"serves). releases is intentionally empty — installable only " +
		"once a released asset is pinned (Track 107). Audit trail: " +
		"market-audit.jsonl under the module dir.",
}

func init() {
	catalog = slices.Insert(catalog, 4, marketCatalogEntry)
}
