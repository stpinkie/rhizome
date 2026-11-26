//go:build economy

package daemon

import (
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/econweb3"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// wireEconSettler attaches the Track 143 web3 close-out rail when the
// operator opted in (mesh.economy.settle_backend = "web3"). The settle
// rail shares the web3 endpoint, wallet, pending queue, and signing
// policy — injected only when configured.
func wireEconSettler(cfg *config.Config, home string, rhizomeMesh *mesh.Mesh) {
	if cfg.Mesh.Economy.SettleBackend != "web3" {
		return
	}
	stack, serr := web3.OpenSigningStack(home, &cfg.Tools.Web3.Signing)
	if serr != nil {
		logger.WarnCF("mesh", "economy web3 signing stack failed; close-out disabled",
			map[string]any{"error": serr.Error()})
		return
	}
	settler, serr := econweb3.New(
		web3.NewProvider(cfg), stack, econweb3.Options{
			Signer:     cfg.Mesh.Economy.SettleSigner,
			From:       cfg.Mesh.Economy.SettleFrom,
			ApproveTTL: cfg.Tools.Web3.Signing.GetApprovalTimeout(),
		})
	if serr != nil {
		logger.WarnCF("mesh", "economy web3 settle backend failed; close-out disabled",
			map[string]any{"error": serr.Error()})
		return
	}
	rhizomeMesh.SetEconSettler(settler)
}

// econAdvertHook returns the pair-bundle economy advert provider. Pair
// bundles echo the node's advertised settlement terms so acceptors see
// the price sheet before trusting.
func econAdvertHook(cfg *config.Config) func() *config.EconAdvert {
	return cfg.Mesh.Economy.Advert
}
