package mesh

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/econweb3"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// Track 143 — web3 close-out over two real meshes + a FakeChain: signed
// payment-committed offer → awaiting_tx ack → transfer through the
// configured signer → phase-2 proof → payee verifies on-chain → both
// ledgers settle under the phase-2 id keyed by the tx hash.

const (
	t143ChainID = "31337"
	t143KeyHex  = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d" // fixture only
	t143Token   = "0x1111111111111111111111111111111111111111"
	t143Payee   = "0x2222222222222222222222222222222222222222"
	t143AltAddr = "0x9999999999999999999999999999999999999999"
)

// t143Econ builds a web3-enabled economy config. The payout is what the
// node advertises as its own settle destination.
func t143Econ(payoutAddr, asset, signer, from string) config.MeshEconomyConfig {
	cfg := config.MeshEconomyConfig{
		Enabled:       true,
		Unit:          "USDC",
		AcceptUnits:   []string{"USDC"},
		SettleBackend: "web3",
		Payout: &config.EconPayout{
			ChainID: t143ChainID, Address: payoutAddr, Asset: asset,
		},
		SettleSigner: signer,
		SettleFrom:   from,
	}
	return cfg
}

// seedT143 records an accrued ledger entry in the given unit.
func seedT143(t *testing.T, m *Mesh, peerID, taskID, amount string, dir econ.Direction) econ.Entry {
	t.Helper()
	rec, err := m.EconomyLedger().Record(econ.Entry{
		PeerID:    peerID,
		Direction: dir,
		TaskID:    taskID,
		Unit:      "USDC",
		Amount:    amount,
		State:     econ.StateAccrued,
		TS:        time.Now().UTC(),
	})
	require.NoError(t, err)
	return *rec
}

// t143Fixture wires two meshes on web3 close-out plus a payer wallet
// stack against a shared FakeChain.
type t143Fixture struct {
	fc    *settlement.FakeChain
	a, b  *Mesh
	stack *web3.SigningStack
	payer string
}

func newT143Fixture(t *testing.T, payerSigner, asset string) *t143Fixture {
	t.Helper()
	t.Setenv("RHIZOME_WALLET_KEYSOURCE", "scrypt")
	t.Setenv("RHIZOME_WALLET_PASSPHRASE", "track143-test")

	fc := settlement.NewFakeChain(t, settlement.RailConfig{ChainID: 31337})
	provider := web3.NewStaticProvider(fc.Endpoint(), "")

	stack, err := web3.OpenSigningStack(t.TempDir(), &config.Web3SigningConfig{
		Enabled:       true,
		FromAddresses: []string{},
		ChainIDs:      []uint64{31337},
	})
	require.NoError(t, err)
	e, err := stack.Wallets.Import(t143KeyHex, "payer")
	require.NoError(t, err)
	stack.Policy.FromAddresses = []string{e.Address}

	cfgA := config.MeshConfig{Enabled: true, Economy: t143Econ(t143AltAddr, asset, payerSigner, e.Address)}
	cfgB := config.MeshConfig{Enabled: true, Economy: t143Econ(t143Payee, asset, "", "")}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	// Payer rail: the configured signer over its own stack.
	beA, err := econweb3.New(provider, stack,
		econweb3.Options{Signer: payerSigner, From: e.Address})
	require.NoError(t, err)
	meshA.SetEconSettler(beA)
	// Payee rail: verify-only (its provider reads the shared chain).
	beB, err := econweb3.New(provider, nil, econweb3.Options{})
	require.NoError(t, err)
	meshB.SetEconSettler(beB)

	// Propagate meshB's advert so meshA sees payout + settle_backends.
	_, err = meshA.TrustAndDiscover(context.Background(), meshB.node.ID())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		stored, ok := meshA.PeerCapabilities(meshB.node.ID())
		return ok && stored.Economy != nil && stored.Economy.Payout != nil &&
			stored.Economy.Payout.Address == t143Payee &&
			peerAdvertisesBackend(stored.Economy, "web3")
	}, 10*time.Second, 100*time.Millisecond, "web3 payout advert must propagate")

	return &t143Fixture{fc: fc, a: meshA, b: meshB, stack: stack, payer: e.Address}
}

func entryT143(t *testing.T, m *Mesh, id string) econ.Entry {
	t.Helper()
	e, ok := m.EconomyLedger().Get(id)
	require.True(t, ok)
	return e
}

func TestTrack143NativeWalletCloseOut(t *testing.T) {
	fx := newT143Fixture(t, "wallet", "native")
	fc, meshA, meshB, payer := fx.fc, fx.a, fx.b, fx.payer
	fc.MintNative(payer, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	ctx := context.Background()
	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	pa := seedT143(t, meshA, bID, "task-w143", "0.25", econ.DirectionPayable)
	rb := seedT143(t, meshB, aID, "task-w143", "0.25", econ.DirectionReceivable)

	res, err := meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, econ.SettleSettled, res.Status)
	assert.True(t, web3.IsHash32(res.TxHash))
	assert.NotEmpty(t, res.SettleID)

	// Payer side: payable settled under the phase-2 id, keyed by tx.
	got := entryT143(t, meshA, pa.EntryID)
	assert.Equal(t, econ.StateSettled, got.State)
	assert.Equal(t, res.SettleID, got.SettleID)
	assert.Equal(t, res.TxHash, got.SettleTX)
	// Payee side: same markers on the receivable.
	got = entryT143(t, meshB, rb.EntryID)
	assert.Equal(t, econ.StateSettled, got.State)
	assert.Equal(t, res.SettleID, got.SettleID)
	assert.Equal(t, res.TxHash, got.SettleTX)

	// Money moved on-chain: 0.25 native at 18 decimals.
	assert.Equal(t, "250000000000000000", fc.NativeBalance(t143Payee).String())

	// Journal: the record is terminal-settled; a re-settle is a no-op.
	recs, err := meshA.inflight.Live()
	require.NoError(t, err)
	assert.Empty(t, recs)
	_, err = meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no accrued payable balance")
}

func TestTrack143ERC20ApprovalCloseOut(t *testing.T) {
	fx := newT143Fixture(t, "approval", t143Token)
	fc, meshA, meshB, stack, payer := fx.fc, fx.a, fx.b, fx.stack, fx.payer
	fc.DeployToken(t143Token)
	fc.SetDecimals(t143Token, 6)
	fc.Mint(t143Token, payer, big.NewInt(1_000_000_000))
	ctx := context.Background()
	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	pa := seedT143(t, meshA, bID, "task-a143", "12.5", econ.DirectionPayable)
	rb := seedT143(t, meshB, aID, "task-a143", "12.5", econ.DirectionReceivable)

	// Phase 1 + queue submit: awaiting human approval, nothing moved.
	res, err := meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, econ.SettleAwaitingApproval, res.Status)
	require.NotEmpty(t, res.PendingID)
	assert.Equal(t, econ.StateAccrued, entryT143(t, meshA, pa.EntryID).State)
	assert.Equal(t, "1000000000", fc.Balance(t143Token, payer).String())

	// A repeat call resumes the same record — no second queue entry.
	res2, err := meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.NoError(t, err)
	assert.Equal(t, res.PendingID, res2.PendingID)
	entries, err := stack.Pending.List()
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	// Operator approves; the pending store executes the transfer.
	_, err = stack.Pending.Resolve(res.PendingID, true, "operator")
	require.NoError(t, err)
	_, err = stack.Pending.ExecuteApproved(ctx, res.PendingID, stack.Wallets,
		web3.NewStaticProvider(fc.Endpoint(), ""), stack.Ledger)
	require.NoError(t, err)

	// The drive picks up the broadcast hash and runs phase 2.
	meshA.settleDriveStep(ctx)

	got := entryT143(t, meshA, pa.EntryID)
	require.Equal(t, econ.StateSettled, got.State)
	require.True(t, web3.IsHash32(got.SettleTX))
	got = entryT143(t, meshB, rb.EntryID)
	require.Equal(t, econ.StateSettled, got.State)
	assert.Equal(t, got.SettleID, entryT143(t, meshA, pa.EntryID).SettleID)
	assert.Equal(t, "12500000", fc.Balance(t143Token, t143Payee).String())
}

func TestTrack143ApprovalRejectFailsRecord(t *testing.T) {
	fx := newT143Fixture(t, "approval", "native")
	fc, meshA, meshB, stack, payer := fx.fc, fx.a, fx.b, fx.stack, fx.payer
	fc.MintNative(payer, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	ctx := context.Background()
	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	pa := seedT143(t, meshA, bID, "task-r143", "0.1", econ.DirectionPayable)
	seedT143(t, meshB, aID, "task-r143", "0.1", econ.DirectionReceivable)

	res, err := meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.NoError(t, err)
	require.Equal(t, econ.SettleAwaitingApproval, res.Status)

	// Operator rejects — the record goes terminal, nothing settles.
	_, err = stack.Pending.Resolve(res.PendingID, false, "operator")
	require.NoError(t, err)
	meshA.settleDriveStep(ctx)
	recs, err := meshA.inflight.List()
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, econ.SettleFailed, recs[0].State)
	assert.Equal(t, econ.StateAccrued, entryT143(t, meshA, pa.EntryID).State)
}

func TestTrack143Phase1DivergedLeavesAccrued(t *testing.T) {
	fx := newT143Fixture(t, "wallet", "native")
	meshA, meshB := fx.a, fx.b
	ctx := context.Background()
	bID := meshB.node.ID().String()

	// A owes B, but B's receivable amount disagrees → settle_reject.
	pa := seedT143(t, meshA, bID, "task-d143", "0.5", econ.DirectionPayable)
	seedT143(t, meshB, meshA.node.ID().String(), "task-d143", "0.75", econ.DirectionReceivable)

	_, err := meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "econ:diverged")
	assert.Equal(t, econ.StateAccrued, entryT143(t, meshA, pa.EntryID).State)
	recs, err := meshA.inflight.List()
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, econ.SettleRejected, recs[0].State)
}

func TestTrack143PayeeWithoutWeb3Advert(t *testing.T) {
	// meshB configured for ledger only — its advert lacks the web3
	// backend flag, so the payer gate fails closed before any offer.
	fx := newT143Fixture(t, "wallet", "native")
	meshA, meshB := fx.a, fx.b
	meshB.cfg.Economy.SettleBackend = "ledger"

	// Re-announce with the downgraded config and wait for propagation.
	_, err := meshA.TrustAndDiscover(context.Background(), meshB.node.ID())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		stored, ok := meshA.PeerCapabilities(meshB.node.ID())
		return ok && stored.Economy != nil &&
			!peerAdvertisesBackend(stored.Economy, "web3")
	}, 10*time.Second, 100*time.Millisecond)

	bID := meshB.node.ID().String()
	seedT143(t, meshA, bID, "task-n143", "0.5", econ.DirectionPayable)
	_, err = meshA.EconomySettleWeb3(context.Background(), bID, "USDC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "web3")
}

func TestTrack143RequiresConfiguredSettler(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: t143Econ(t143AltAddr, "native", "wallet", t143AltAddr)}
	cfgB := config.MeshConfig{Enabled: true, Economy: t143Econ(t143Payee, "native", "", "")}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	seedT143(t, meshA, bID, "task-c143", "0.5", econ.DirectionPayable)
	_, err := meshA.EconomySettleWeb3(context.Background(), bID, "USDC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
