// Package settlement implements the v0.14.0 Track 101 escrow/settlement
// abstraction for the open agent work market: a SettlementRail interface
// with a Smart Invoice-backed JSON-RPC implementation (RPCRail), an
// in-memory fixture (MockRail), and a fake JSON-RPC endpoint (FakeChain)
// for tests. Track 102 (sell-side) calls VerifyLock before spending LLM
// budget; Track 103 (buy-side) calls Open/Release. Smart Invoice has no
// unilateral seller-claim path — see docs/design/escrow-survey.md for the
// claim→lock+resolve semantic mapping and the trade-off register.
package settlement

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// Terms are the per-session escrow facts both sides verify. They mirror
// what a Smart Invoice escrow stores on-chain: the buyer is the contract
// client, the seller is the provider, and the dispute window is
// expressed as an absolute terminationTime after which the client may
// safety-valve withdraw the balance.
type Terms struct {
	Buyer           string   `json:"buyer"`                      // escrow client() — funds depositor, release() caller
	Seller          string   `json:"seller"`                     // escrow provider() — create recipient
	Token           string   `json:"token"`                      // ERC-20 asset address
	Amount          *big.Int `json:"amount"`                     // single-milestone total in token base units
	TaskHash        [32]byte `json:"task_hash"`                  // work reference -> escrow details field
	TerminationTime int64    `json:"termination_time,omitempty"` // unix secs; 0 = derive at Open
}

// Validate checks Terms are well-formed before rail calls.
func (t Terms) Validate() error {
	switch {
	case !web3.IsAddress(t.Buyer):
		return fmt.Errorf("terms: buyer %q is not a 0x address", t.Buyer)
	case !web3.IsAddress(t.Seller):
		return fmt.Errorf("terms: seller %q is not a 0x address", t.Seller)
	case !web3.IsAddress(t.Token):
		return fmt.Errorf("terms: token %q is not a 0x address", t.Token)
	case t.Amount == nil || t.Amount.Sign() <= 0:
		return fmt.Errorf("terms: amount must be a positive integer")
	case t.TerminationTime < 0:
		return fmt.Errorf("terms: termination_time cannot be negative")
	}
	return nil
}

// Rail kind names — the module's `escrow_rail` field value.
const (
	// RailKindSmartInvoice is the v0.14.0 pick: Smart Invoice
	// factory clones, session_id = clone address.
	RailKindSmartInvoice = "smart_invoice"
	// RailKindRhizome is the v0.16.0 Track 127 graduated contract:
	// contracts/RhizomeEscrow.sol, session-keyed single deployment,
	// session_id = bytes32 keccak256(correlationID).
	RailKindRhizome = "rhizome"
)

// RailConfig is the rail-level configuration resolved from the module's
// escrow_* fields (see ConfigFromFields).
type RailConfig struct {
	Kind              string // escrow_rail — smart_invoice (default) | rhizome
	ChainID           uint64 // escrow_chain_id — EVM chain the rail talks to
	Factory           string // escrow_contract — factory (smart_invoice) or RhizomeEscrow (rhizome)
	Token             string // escrow_token — default payment asset
	Arbiter           string // escrow_arbiter — resolver/arbiter address
	DisputeWindowSecs int64  // escrow_dispute_window — seconds from Open to the dispute deadline
	WrappedNative     string // wrapped native token for the chain (smart_invoice init-time requirement)
}

// escrow_* module field names (flat ConfigField strings, per the market
// module field schema in docs/design/v0.14.0-sprint.md).
const (
	FieldRail          = "escrow_rail"
	FieldChainID       = "escrow_chain_id"
	FieldContract      = "escrow_contract"
	FieldToken         = "escrow_token"
	FieldArbiter       = "escrow_arbiter"
	FieldDisputeWindow = "escrow_dispute_window"
)

// ConfigFromFields resolves the module's escrow_* fields into a
// RailConfig. It returns (nil, nil) when escrow_contract is unset — the
// spec's "fixture rail stays the shipped default" posture — and an error
// when a partial or malformed configuration is provided. All-or-nothing:
// a configured contract requires every field the on-chain init needs.
func ConfigFromFields(fields map[string]string) (*RailConfig, error) {
	get := func(k string) string { return strings.TrimSpace(fields[k]) }
	contract := get(FieldContract)
	if contract == "" {
		return nil, nil
	}
	if !web3.IsAddress(contract) {
		return nil, fmt.Errorf("%s %q is not a 0x address", FieldContract, contract)
	}
	kind := get(FieldRail)
	if kind == "" {
		kind = RailKindSmartInvoice
	}
	if kind != RailKindSmartInvoice && kind != RailKindRhizome {
		return nil, fmt.Errorf(
			"%s %q must be %s or %s", FieldRail, kind, RailKindSmartInvoice, RailKindRhizome)
	}
	chainStr := get(FieldChainID)
	chainID, err := strconv.ParseUint(chainStr, 10, 64)
	if err != nil || chainID == 0 {
		return nil, fmt.Errorf("%s %q is not a positive chain id", FieldChainID, chainStr)
	}
	token := get(FieldToken)
	if !web3.IsAddress(token) {
		return nil, fmt.Errorf("%s %q is not a 0x address (required with %s)", FieldToken, token, FieldContract)
	}
	arbiter := get(FieldArbiter)
	if !web3.IsAddress(arbiter) {
		return nil, fmt.Errorf("%s %q is not a 0x address (required with %s)", FieldArbiter, arbiter, FieldContract)
	}
	windowStr := get(FieldDisputeWindow)
	window, err := strconv.ParseInt(windowStr, 10, 64)
	if err != nil || window <= 0 {
		return nil, fmt.Errorf("%s %q is not a positive seconds value", FieldDisputeWindow, windowStr)
	}
	// Wrapped native is a Smart Invoice init-time requirement — the
	// graduated contract is ERC-20-only and doesn't take one.
	var wrapped string
	if kind == RailKindSmartInvoice {
		wrapped, err = WrappedNativeForChain(chainID)
		if err != nil {
			return nil, err
		}
	}
	return &RailConfig{
		Kind:              kind,
		ChainID:           chainID,
		Factory:           contract,
		Token:             token,
		Arbiter:           arbiter,
		DisputeWindowSecs: window,
		WrappedNative:     wrapped,
	}, nil
}

// InvoiceTypeEscrow is the Smart Invoice factory's bytes32 type key for
// the escrow implementation (the label registered in the Sepolia
// factory's `escrow` implementation slot).
var InvoiceTypeEscrow = bytes32Label("escrow")

// CorrelationSalt derives the createDeterministic salt from the buyer's
// correlation reference (the id passed to Open / the pre-session
// presentation ref). keccak256 keeps it a uniform bytes32.
func CorrelationSalt(correlationID string) [32]byte {
	var s [32]byte
	copy(s[:], web3.Keccak256([]byte(correlationID)))
	return s
}

func bytes32Label(s string) [32]byte {
	var b [32]byte
	copy(b[:], s)
	return b
}

// wrappedNativeByChain maps chain id -> canonical wrapped-native token
// address. Smart Invoice's init requires a non-zero wrappedNativeToken
// even for pure ERC-20 escrows (it backs the receive() native-deposit
// path); an unmapped chain fails closed rather than guessing.
var wrappedNativeByChain = map[uint64]string{
	1:        "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", // mainnet WETH
	11155111: "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14", // sepolia WETH
	100:      "0xe91D153E0b41518A2Ce8Dd3D7944Fa863463a97d", // gnosis WXDAI
	137:      "0x0d500B1d8E8eF31E21C99d1Db9A6444d3ADf1270", // polygon WPOL
	42161:    "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1", // arbitrum one WETH
	8453:     "0x4200000000000000000000000000000000000006", // base WETH
	421614:   "0x980B62Da83eFf3D4576C647993b0c1D7fef17c73", // arb sepolia WETH
	84532:    "0x4200000000000000000000000000000000000006", // base sepolia WETH
}

// WrappedNativeForChain returns the canonical wrapped-native token for
// chainID, or an error for chains the table does not know.
func WrappedNativeForChain(chainID uint64) (string, error) {
	if w, ok := wrappedNativeByChain[chainID]; ok {
		return w, nil
	}
	return "", fmt.Errorf("no known wrapped-native token for chain %d — extend wrappedNativeByChain", chainID)
}

// DefaultResolutionRate is the fee divisor Smart Invoice applies when a
// resolver has no registered rate on the factory: resolutionFee =
// balance / resolutionRate (20 -> 5% of the locked balance to the
// resolver on resolve()).
const DefaultResolutionRate = 20
