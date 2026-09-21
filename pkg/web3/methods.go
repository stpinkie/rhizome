// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

// readOnlyMethods is the hard-coded allowlist for the web3_rpc passthrough
// tool. Anything not enumerated is denied — sends, signing, account
// enumeration, subscriptions, and administrative/debug namespaces are all
// excluded by absence.
//
// eth_accounts is intentionally absent: on remote/shared endpoints it can
// enumerate key custody, which is not a read we want an agent to make.
// eth_estimateGas is included — it simulates but does not broadcast.
var readOnlyMethods = map[string]struct{}{
	"eth_blobGasFee":                          {},
	"eth_blockNumber":                         {},
	"eth_call":                                {},
	"eth_chainId":                             {},
	"eth_coinbase":                            {},
	"eth_createAccessList":                    {},
	"eth_estimateGas":                         {},
	"eth_feeHistory":                          {},
	"eth_gasPrice":                            {},
	"eth_getBalance":                          {},
	"eth_getBlockByHash":                      {},
	"eth_getBlockByNumber":                    {},
	"eth_getBlockTransactionCountByHash":      {},
	"eth_getBlockTransactionCountByNumber":    {},
	"eth_getCode":                             {},
	"eth_getLogs":                             {},
	"eth_getProof":                            {},
	"eth_getStorageAt":                        {},
	"eth_getTransactionByBlockHashAndIndex":   {},
	"eth_getTransactionByBlockNumberAndIndex": {},
	"eth_getTransactionByHash":                {},
	"eth_getTransactionCount":                 {},
	"eth_getTransactionReceipt":               {},
	"eth_getUncleCountByBlockHash":            {},
	"eth_getUncleCountByBlockNumber":          {},
	"eth_maxPriorityFeePerGas":                {},
	"eth_mining":                              {},
	"eth_syncing":                             {},
	"net_listening":                           {},
	"net_peerCount":                           {},
	"net_version":                             {},
	"web3_clientVersion":                      {},
	"web3_sha3":                               {},
}

// IsReadOnlyMethod reports whether method is on the read-only allowlist.
func IsReadOnlyMethod(method string) bool {
	_, ok := readOnlyMethods[method]
	return ok
}
