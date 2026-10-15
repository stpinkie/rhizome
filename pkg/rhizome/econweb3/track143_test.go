// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package econweb3

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// Track 143 — the close-out rail over FakeChain: decimal→base-unit
// conversion, wallet sign + broadcast, the pending-approval queue, and
// the payee's on-chain verification (chain/from/destination/amount).

// testKeyHex is a fixture key — never a real wallet.
const testKeyHex = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

const (
	testChainID = 31337
	testToken   = "0x1111111111111111111111111111111111111111"
	testPayee   = "0x2222222222222222222222222222222222222222"
)

// newRailFixture builds a FakeChain + signing stack + backend. The
// signing policy allows the payer address on the fake chain only.
func newRailFixture(t *testing.T, signer string) (*settlement.FakeChain, *web3.SigningStack, *Backend, string) {
	t.Helper()
	t.Setenv("RHIZOME_WALLET_KEYSOURCE", "scrypt")
	t.Setenv("RHIZOME_WALLET_PASSPHRASE", "track143-test")

	fc := settlement.NewFakeChain(t, settlement.RailConfig{ChainID: testChainID})
	home := t.TempDir()
	stack, err := web3.OpenSigningStack(home, &config.Web3SigningConfig{
		Enabled:       true,
		FromAddresses: []string{}, // populated after the wallet import below
		ChainIDs:      []uint64{testChainID},
	})
	require.NoError(t, err)
	e, err := stack.Wallets.Import(testKeyHex, "payer")
	require.NoError(t, err)
	stack.Policy.FromAddresses = []string{e.Address}

	b, err := New(web3.NewStaticProvider(fc.Endpoint(), ""), stack,
		Options{Signer: signer, From: e.Address})
	require.NoError(t, err)
	return fc, stack, b, e.Address
}

func paymentFor(from, asset string) *econ.SettlePayment {
	return &econ.SettlePayment{
		ChainID: "31337", Address: testPayee, Asset: asset, From: from,
	}
}

func TestBaseUnitsConversion(t *testing.T) {
	v, err := baseUnits("2.5", 6)
	require.NoError(t, err)
	assert.Equal(t, "2500000", v.String())

	v, err = baseUnits("0.000001", 6)
	require.NoError(t, err)
	assert.Equal(t, "1", v.String())

	v, err = baseUnits("1", 18)
	require.NoError(t, err)
	assert.Equal(t, "1000000000000000000", v.String())

	_, err = baseUnits("0.0000001", 6)
	assert.Error(t, err, "finer than the asset's base unit must refuse")
	_, err = baseUnits("abc", 6)
	assert.Error(t, err)
	_, err = baseUnits("-1", 6)
	assert.Error(t, err)
}

func TestNativeWalletSignBroadcastVerify(t *testing.T) {
	fc, _, b, payer := newRailFixture(t, "wallet")
	ctx := context.Background()
	fc.MintNative(payer, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) // 1 ETH

	p := paymentFor(payer, "native")
	raw, hash, err := b.SignRaw(ctx, p, "0.25")
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	require.True(t, web3.IsHash32(hash))

	got, err := b.Broadcast(ctx, raw)
	require.NoError(t, err)
	assert.Equal(t, hash, got)
	require.NoError(t, b.Verify(ctx, p, "0.25", hash))
	assert.Equal(t, "250000000000000000", fc.NativeBalance(testPayee).String())

	// Re-broadcast of the same raw is already-known, not a second pay.
	got2, err := b.Broadcast(ctx, raw)
	require.NoError(t, err)
	assert.Equal(t, hash, got2)
	assert.Equal(t, "250000000000000000", fc.NativeBalance(testPayee).String())
}

func TestERC20WalletSignBroadcastVerify(t *testing.T) {
	fc, _, b, payer := newRailFixture(t, "wallet")
	ctx := context.Background()
	fc.DeployToken(testToken)
	fc.SetDecimals(testToken, 6)
	fc.Mint(testToken, payer, big.NewInt(1_000_000_000)) // 1000.000000

	p := paymentFor(payer, testToken)
	raw, hash, err := b.SignRaw(ctx, p, "12.5")
	require.NoError(t, err)
	got, err := b.Broadcast(ctx, raw)
	require.NoError(t, err)
	assert.Equal(t, hash, got)
	require.NoError(t, b.Verify(ctx, p, "12.5", hash))
	assert.Equal(t, "12500000", fc.Balance(testToken, testPayee).String())
}

func TestQueueSubmitExecuteVerify(t *testing.T) {
	fc, stack, b, payer := newRailFixture(t, "approval")
	ctx := context.Background()
	fc.DeployToken(testToken)
	fc.SetDecimals(testToken, 6)
	fc.Mint(testToken, payer, big.NewInt(1_000_000_000))

	p := paymentFor(payer, testToken)
	require.NoError(t, b.Queue(ctx, p, "12.5", "deadbeefcafef00d"))

	// The queue entry is the contract call the approver reviews.
	entries, err := stack.Pending.List()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, "deadbeefcafef00d", e.ID)
	assert.Equal(t, web3.KindContract, e.Kind)
	assert.Equal(t, testToken, e.To, "queue target is the token contract")
	assert.Contains(t, e.Data, "a9059cbb", "transfer() selector")
	assert.Equal(t, uint64(testChainID), e.ChainID)

	// Still pending → QueuedTx waits.
	_, done, err := b.QueuedTx(e.ID)
	require.NoError(t, err)
	assert.False(t, done)

	// Approve + execute through the real pending store path.
	_, err = stack.Pending.Resolve(e.ID, true, "operator")
	require.NoError(t, err)
	_, err = stack.Pending.ExecuteApproved(ctx, e.ID, stack.Wallets,
		web3.NewStaticProvider(fc.Endpoint(), ""), stack.Ledger)
	require.NoError(t, err)

	hash, done, err := b.QueuedTx(e.ID)
	require.NoError(t, err)
	require.True(t, done)
	require.True(t, web3.IsHash32(hash))
	require.NoError(t, b.Verify(ctx, p, "12.5", hash))
	assert.Equal(t, "12500000", fc.Balance(testToken, testPayee).String())
}

func TestQueueDuplicateIDRefuses(t *testing.T) {
	_, _, b, payer := newRailFixture(t, "approval")
	p := paymentFor(payer, "native")
	err := b.Queue(context.Background(), p, "1", "deadbeefcafef00d")
	require.NoError(t, err)
	err = b.Queue(context.Background(), p, "1", "deadbeefcafef00d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestQueuePolicyDeny(t *testing.T) {
	fc, stack, b, payer := newRailFixture(t, "approval")
	_ = fc
	stack.Policy.FromAddresses = []string{"0x9999999999999999999999999999999999999999"}
	err := b.Queue(context.Background(), paymentFor(payer, "native"), "1", "deadbeefcafef00d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "policy denied")
}

func TestVerifyRejectsMismatches(t *testing.T) {
	fc, _, b, payer := newRailFixture(t, "wallet")
	ctx := context.Background()
	fc.MintNative(payer, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))

	p := paymentFor(payer, "native")
	raw, hash, err := b.SignRaw(ctx, p, "0.25")
	require.NoError(t, err)
	_, err = b.Broadcast(ctx, raw)
	require.NoError(t, err)

	// Wrong destination commitment.
	bad := *p
	bad.Address = "0x3333333333333333333333333333333333333333"
	assert.Error(t, b.Verify(ctx, &bad, "0.25", hash))

	// Wrong amount.
	assert.Error(t, b.Verify(ctx, p, "0.5", hash))

	// Wrong sender commitment (grafted hash defense).
	badFrom := *p
	badFrom.From = "0x4444444444444444444444444444444444444444"
	assert.Error(t, b.Verify(ctx, &badFrom, "0.25", hash))

	// Wrong chain commitment.
	badChain := *p
	badChain.ChainID = "1"
	assert.Error(t, b.Verify(ctx, &badChain, "0.25", hash))

	// Malformed and unknown hashes — unknown is retryable pending.
	assert.Error(t, b.Verify(ctx, p, "0.25", "0x1234"))
	err = b.Verify(ctx, p, "0.25",
		"0x00000000000000000000000000000000000000000000000000000000000000ff")
	require.Error(t, err)
	assert.True(t, econ.IsVerifyPending(err), "unknown hash must be retryable, got %v", err)
}

func TestSignRawChainPinRefuses(t *testing.T) {
	_, _, b, payer := newRailFixture(t, "wallet")
	p := paymentFor(payer, "native")
	p.ChainID = "1" // offer committed to mainnet; endpoint is 31337
	_, _, err := b.SignRaw(context.Background(), p, "0.25")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chain pin")
}

// A nil payment commitment (e.g. a journaled record with payment:null)
// must error, never panic — Queue and SignRaw dereference p.From before
// paymentTx's own nil check.
func TestNilPaymentCommitmentGuard(t *testing.T) {
	_, _, b, _ := newRailFixture(t, "approval")
	err := b.Queue(context.Background(), nil, "1", "deadbeefcafef00d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing payment commitment")

	_, _, bw, _ := newRailFixture(t, "wallet")
	_, _, err = bw.SignRaw(context.Background(), nil, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing payment commitment")
}

func TestSignerModeDefaultAndValidate(t *testing.T) {
	b, err := New(nil, nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, "approval", b.SignerMode())

	_, err = New(nil, nil, Options{Signer: "bogus"})
	assert.Error(t, err)
	_, err = New(nil, nil, Options{From: "not-an-address"})
	assert.Error(t, err)
}
