// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package settlement

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// testKey is a fixture private key — tests only, never a real wallet.
const testKeyHex = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

func walletFixture(t *testing.T) (*web3.WalletStore, string) {
	t.Helper()
	t.Setenv("RHIZOME_WALLET_KEYSOURCE", "scrypt")
	t.Setenv("RHIZOME_WALLET_PASSPHRASE", "track104-test")
	store := web3.OpenWalletStore(t.TempDir())
	e, err := store.Import(testKeyHex, "buyer")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return store, e.Address
}

func TestWalletSender_SignsAndLands(t *testing.T) {
	fc := NewFakeChain(t, RailConfig{
		ChainID: 11155111, Factory: "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		WrappedNative: "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14",
	})
	store, buyer := walletFixture(t)
	ctx := context.Background()

	// Open the escrow through the unlocked transport (create+transfer);
	// the wallet sender proves itself on release — a signed raw tx.
	client := web3.NewClient(fc.Endpoint(), "", nil)
	direct := NewDirectSender(client, buyer)
	rail, err := NewRPCRail(RailConfig{
		ChainID: fc.chainID, Factory: fc.factory,
		Token:             "0x1111111111111111111111111111111111111111",
		Arbiter:           "0x2222222222222222222222222222222222222222",
		DisputeWindowSecs: 3600,
		WrappedNative:     fc.wrapped,
	}, direct)
	if err != nil {
		t.Fatalf("rail: %v", err)
	}
	fc.DeployToken("0x1111111111111111111111111111111111111111")
	fc.Mint("0x1111111111111111111111111111111111111111", buyer, big.NewInt(1_000_000))
	var taskHash [32]byte
	copy(taskHash[:], web3.Keccak256([]byte("task")))
	sessionID := fc.EscrowAddr("corr-wallet-1")
	if _, err := rail.Open(ctx, "corr-wallet-1", Terms{
		Buyer: buyer, Seller: "0x3333333333333333333333333333333333333333",
		Token:  "0x1111111111111111111111111111111111111111",
		Amount: big.NewInt(500_000), TaskHash: taskHash,
		TerminationTime: time.Now().Unix() + 3600,
	}); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Wallet-signed release — the module's Track-104 posture.
	ws := NewWalletSender(
		client, web3.NewStaticProvider(fc.Endpoint(), ""), store, buyer, fc.chainID)
	wrail, err := NewRPCRail(RailConfig{
		ChainID: fc.chainID, Factory: fc.factory,
		Token:             "0x1111111111111111111111111111111111111111",
		Arbiter:           "0x2222222222222222222222222222222222222222",
		DisputeWindowSecs: 3600,
		WrappedNative:     fc.wrapped,
	}, ws)
	if err != nil {
		t.Fatalf("wrail: %v", err)
	}
	tx, err := wrail.Release(ctx, sessionID)
	if err != nil {
		t.Fatalf("wallet-signed release: %v", err)
	}
	if tx == "" {
		t.Fatal("release returned no tx hash")
	}
	e, ok := fc.EscrowSnapshot(sessionID)
	if !ok || e.released.Sign() == 0 {
		t.Fatalf("escrow not released on-chain: %+v", e)
	}
}

func TestWalletSender_ChainPinRefuses(t *testing.T) {
	fc := NewFakeChain(t, RailConfig{
		ChainID: 11155111, Factory: "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		WrappedNative: "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14",
	})
	store, buyer := walletFixture(t)
	client := web3.NewClient(fc.Endpoint(), "", nil)
	// Pinned to chain 1 but the endpoint serves 11155111 — must refuse
	// before signing, not broadcast onto the wrong chain.
	ws := NewWalletSender(
		client, web3.NewStaticProvider(fc.Endpoint(), ""), store, buyer, 1)
	_, err := ws.SendTx(context.Background(),
		"0x1111111111111111111111111111111111111111",
		[]byte{0xde, 0xad, 0xbe, 0xef}, nil)
	if err == nil {
		t.Fatal("chain-pin mismatch must refuse")
	}
}

func TestWalletSender_MissingKeyRefuses(t *testing.T) {
	fc := NewFakeChain(t, RailConfig{
		ChainID: 11155111, Factory: "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		WrappedNative: "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14",
	})
	store, _ := walletFixture(t)
	client := web3.NewClient(fc.Endpoint(), "", nil)
	ws := NewWalletSender(
		client, web3.NewStaticProvider(fc.Endpoint(), ""), store,
		"0x9999999999999999999999999999999999999999", fc.chainID)
	_, err := ws.SendTx(context.Background(),
		"0x1111111111111111111111111111111111111111",
		[]byte{0xde, 0xad, 0xbe, 0xef}, nil)
	if err == nil {
		t.Fatal("missing wallet key must refuse")
	}
}
