//go:build !economy

package mesh

import (
	"context"

	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
)

// economyBuild is false in the default build — bucket-B (cross-user
// economy) surfaces compile to inert stubs. See ALIGNMENT.md §3.
const economyBuild = false

// The methods below mirror the economy-tagged implementations in
// econ.go/econobs.go/econproto.go/econweb3.go. Untagged callers keep their
// call sites; the stubs make every economy path a no-op. Wire fields
// (econ/charge/max_cost) remain in the schema but are never populated.

func (m *Mesh) startEcon() {}

func (m *Mesh) settleWatchLoop(ctx context.Context) {}

func (m *Mesh) econWatchLoop(ctx context.Context) {}

func (m *Mesh) recordEconOutcome(pidStr, op, outcome, sessionID, valueHash string) {
}

func (m *Mesh) recordEconOp(pidStr, op string, success bool, callErr error) {}

func (m *Mesh) peerEconView(peerID string) *econ.PeerSummary { return nil }

func chargeOutcomeHash(c *econ.Charge) string { return "" }

func (m *Mesh) checkSettleDue(peerID string, charge *econ.Charge) {}

func (m *Mesh) EconQuoteFor(offerer, task string) string { return "" }

func (m *Mesh) SetEconSettler(s econ.Settler) {}
