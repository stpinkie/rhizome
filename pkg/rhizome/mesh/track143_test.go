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

// A phase-2 ack lost after the payee committed (settle_tx written) must
// not turn a completed settlement into a divergence: the retried offer —
// a fresh document with a new nonce/id — re-acks against the rows already
// settled under the same tx, and the payer converges to settled.
func TestTrack143Phase2RedeliveryReAcks(t *testing.T) {
	fx := newT143Fixture(t, "approval", "native")
	fc, meshA, meshB, stack, payer := fx.fc, fx.a, fx.b, fx.stack, fx.payer
	fc.MintNative(payer, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	ctx := context.Background()
	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	pa := seedT143(t, meshA, bID, "task-l143", "0.2", econ.DirectionPayable)
	rb := seedT143(t, meshB, aID, "task-l143", "0.2", econ.DirectionReceivable)

	res, err := meshA.EconomySettleWeb3(ctx, bID, "USDC")
	require.NoError(t, err)
	require.Equal(t, econ.SettleAwaitingApproval, res.Status)

	// Operator approves; the transfer executes for real on FakeChain.
	_, err = stack.Pending.Resolve(res.PendingID, true, "operator")
	require.NoError(t, err)
	_, err = stack.Pending.ExecuteApproved(ctx, res.PendingID, stack.Wallets,
		web3.NewStaticProvider(fc.Endpoint(), ""), stack.Ledger)
	require.NoError(t, err)

	// Land the record in broadcast: driveInflight would run phase 2 to
	// completion in one pass, so step the awaiting_approval state only.
	rec, err := meshA.inflightRecord(bID, "USDC")
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.Equal(t, econ.SettleAwaitingApproval, rec.State)
	advanced, res2, err := meshA.stepInflight(ctx, rec)
	require.NoError(t, err)
	require.Nil(t, res2)
	require.True(t, advanced)

	rec, err = meshA.inflightRecord(bID, "USDC")
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.Equal(t, econ.SettleBroadcast, rec.State)
	require.True(t, web3.IsHash32(rec.TxHash))

	// The lost ack: deliver a phase-2 offer straight to the payee's
	// handler — its receivables settle under the tx — and discard the
	// response as if the frame never reached the payer.
	offer2, err := econ.Phase2Offer(rec.Offer, rec.TxHash)
	require.NoError(t, err)
	require.NoError(t, offer2.Sign(meshA.id.PrivateKey))
	resp := meshB.handleSettleOfferWeb3(meshA.node.ID(), offer2, time.Now())
	require.True(t, resp.OK)
	require.True(t, resp.Accepted)
	require.NotEmpty(t, resp.SettleID)
	require.Equal(t, econ.StateSettled, entryT143(t, meshB, rb.EntryID).State)
	require.Equal(t, rec.TxHash, entryT143(t, meshB, rb.EntryID).SettleTX)

	// The payer retries phase 2 with a new offer document. The payee
	// must re-ack the committed settlement — not reject it.
	meshA.settleDriveStep(ctx)

	got := entryT143(t, meshA, pa.EntryID)
	require.Equal(t, econ.StateSettled, got.State)
	require.Equal(t, rec.TxHash, got.SettleTX)
	// The payer's settle id is the retry offer's; the payee keeps the
	// original. Both anchor the same on-chain tx — that is the shared ref.
	require.NotEqual(t, resp.SettleID, got.SettleID)

	recs, err := meshA.inflight.List()
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, econ.SettleSettled, recs[0].State)

	// No false divergence: no disputed econ_settle outcome on either
	// side, and the payee's completed outcome stands alone.
	for _, tc := range []struct {
		m    *Mesh
		pidS string
	}{{meshA, bID}, {meshB, aID}} {
		sc, ok := tc.m.scoreStore.Get(mustPID(t, tc.pidS))
		if !ok {
			continue
		}
		for _, o := range sc.Outcomes {
			require.False(t, o.Op == OpEconSettle && o.Outcome == OutcomeDisputed,
				"redelivery must not record a disputed settle outcome")
		}
	}
	sc, ok := meshB.scoreStore.Get(mustPID(t, aID))
	require.True(t, ok)
	require.Equal(t, 1, sc.OpStats[OpEconSettle].Successes)
	require.Equal(t, 0, sc.OpStats[OpEconSettle].Failures)
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
