// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import "testing"

func TestIsReadOnlyMethod(t *testing.T) {
	allowed := []string{
		"eth_chainId", "eth_blockNumber", "eth_call", "eth_getBalance",
		"eth_getLogs", "eth_getProof", "eth_getTransactionReceipt",
		"eth_estimateGas", "eth_feeHistory", "net_version", "net_peerCount",
		"web3_clientVersion", "web3_sha3",
	}
	for _, m := range allowed {
		if !IsReadOnlyMethod(m) {
			t.Fatalf("IsReadOnlyMethod(%q) = false, want true", m)
		}
	}
	denied := []string{
		// State-changing / signing
		"eth_sendTransaction", "eth_sendRawTransaction", "eth_sign",
		"eth_signTransaction", "personal_sign", "personal_sendTransaction",
		// Account custody enumeration
		"eth_accounts", "eth_requestAccounts",
		// Subscriptions and admin/debug namespaces
		"eth_subscribe", "eth_unsubscribe", "admin_peers",
		"debug_traceTransaction", "txpool_content", "miner_start",
		// Unknowns and near-misses
		"", "eth_getBalance ", "ETH_chainId", "eth_send",
	}
	for _, m := range denied {
		if IsReadOnlyMethod(m) {
			t.Fatalf("IsReadOnlyMethod(%q) = true, want false", m)
		}
	}
}
