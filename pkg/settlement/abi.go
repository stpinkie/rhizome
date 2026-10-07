package settlement

import (
	"fmt"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// Bundled contract ABIs (the "abireg" precedent: raw JSON embedded in
// the package, parsed once at init). Authored from the canonical Smart
// Invoice interfaces on the develop branch:
//
//	apps/contracts/contracts/interfaces/ISmartInvoiceFactory.sol
//	apps/contracts/contracts/interfaces/ISmartInvoiceEscrow.sol
//	apps/contracts/contracts/SmartInvoiceEscrow.sol  (public state getters)
//
// Track 101 ships ABI only — the .sol sources arrive with Track 104's
// binding + E2E. Event entries are carried for documentation of the
// eth_getLogs surface; pkg/web3's parser ignores non-function entries.
//
// Canonical Sepolia deployments (apps/contracts/deployments/sepolia.json):
//
//	factory:            0x8227b9868e00B8eE951F17B480D369b84Cd17c20
//	escrow impl (type "escrow"): 0x49B76dE305933d75fC0eAd6ef090F555bcCD9735
const (
	// SepoliaFactory is the canonical Smart Invoice factory on Sepolia.
	SepoliaFactory = "0x8227b9868e00B8eE951F17B480D369b84Cd17c20"
	// SepoliaEscrowImpl is the registered "escrow" implementation the
	// Sepolia factory clones (informational; the factory resolves it).
	SepoliaEscrowImpl = "0x49B76dE305933d75fC0eAd6ef090F555bcCD9735"
	// SepoliaChainID is the EVM chain id for the canonical deployment.
	SepoliaChainID = 11155111
)

const factoryABIJSON = `[
  {"type":"function","name":"create","stateMutability":"nonpayable",
   "inputs":[{"name":"_recipient","type":"address"},{"name":"_amounts","type":"uint256[]"},{"name":"_data","type":"bytes"},{"name":"_type","type":"bytes32"}],
   "outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"createDeterministic","stateMutability":"nonpayable",
   "inputs":[{"name":"_recipient","type":"address"},{"name":"_amounts","type":"uint256[]"},{"name":"_data","type":"bytes"},{"name":"_type","type":"bytes32"},{"name":"_salt","type":"bytes32"}],
   "outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"predictDeterministicAddress","stateMutability":"view",
   "inputs":[{"name":"_type","type":"bytes32"},{"name":"_salt","type":"bytes32"}],
   "outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"resolutionRateOf","stateMutability":"view",
   "inputs":[{"name":"_resolver","type":"address"}],
   "outputs":[{"name":"","type":"uint256"}]},
  {"type":"event","name":"LogNewInvoice",
   "inputs":[{"name":"invoiceId","type":"uint256","indexed":true},{"name":"invoiceAddress","type":"address","indexed":true},{"name":"amounts","type":"uint256[]","indexed":false},{"name":"invoiceType","type":"bytes32","indexed":true},{"name":"version","type":"uint256","indexed":false}]}
]`

const escrowABIJSON = `[
  {"type":"function","name":"init","stateMutability":"nonpayable",
   "inputs":[{"name":"_recipient","type":"address"},{"name":"_amounts","type":"uint256[]"},{"name":"_data","type":"bytes"}],"outputs":[]},
  {"type":"function","name":"token","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"client","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"provider","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"resolver","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"resolverType","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint8"}]},
  {"type":"function","name":"terminationTime","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"resolutionRate","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"details","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"bytes32"}]},
  {"type":"function","name":"total","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"released","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"locked","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"milestone","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"disputeId","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"wrappedNativeToken","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"getAmounts","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256[]"}]},
  {"type":"function","name":"release","stateMutability":"nonpayable","inputs":[],"outputs":[]},
  {"type":"function","name":"release","stateMutability":"nonpayable","inputs":[{"name":"_milestone","type":"uint256"}],"outputs":[]},
  {"type":"function","name":"releaseTokens","stateMutability":"nonpayable","inputs":[{"name":"_token","type":"address"}],"outputs":[]},
  {"type":"function","name":"addMilestones","stateMutability":"nonpayable","inputs":[{"name":"_milestones","type":"uint256[]"}],"outputs":[]},
  {"type":"function","name":"verify","stateMutability":"nonpayable","inputs":[],"outputs":[]},
  {"type":"function","name":"withdraw","stateMutability":"nonpayable","inputs":[],"outputs":[]},
  {"type":"function","name":"withdrawTokens","stateMutability":"nonpayable","inputs":[{"name":"_token","type":"address"}],"outputs":[]},
  {"type":"function","name":"lock","stateMutability":"payable","inputs":[{"name":"_details","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"resolve","stateMutability":"nonpayable",
   "inputs":[{"name":"_clientAward","type":"uint256"},{"name":"_providerAward","type":"uint256"},{"name":"_details","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"rule","stateMutability":"nonpayable",
   "inputs":[{"name":"_disputeId","type":"uint256"},{"name":"_ruling","type":"uint256"}],"outputs":[]},
  {"type":"event","name":"Deposit","inputs":[{"name":"sender","type":"address","indexed":true},{"name":"amount","type":"uint256","indexed":false}]},
  {"type":"event","name":"Release","inputs":[{"name":"milestone","type":"uint256","indexed":false},{"name":"amount","type":"uint256","indexed":false}]},
  {"type":"event","name":"Withdraw","inputs":[{"name":"balance","type":"uint256","indexed":false}]},
  {"type":"event","name":"Lock","inputs":[{"name":"sender","type":"address","indexed":true},{"name":"details","type":"bytes32","indexed":false}]},
  {"type":"event","name":"Resolve","inputs":[{"name":"resolver","type":"address","indexed":true},{"name":"clientAward","type":"uint256","indexed":false},{"name":"providerAward","type":"uint256","indexed":false},{"name":"resolutionFee","type":"uint256","indexed":false},{"name":"details","type":"bytes32","indexed":false}]},
  {"type":"event","name":"Verified","inputs":[{"name":"client","type":"address","indexed":true},{"name":"invoice","type":"address","indexed":true}]}
]`

// Minimal ERC-20 surface the rail needs: funding transfers plus the
// balance/allowance views VerifyLock consults.
const erc20ABIJSON = `[
  {"type":"function","name":"transfer","stateMutability":"nonpayable",
   "inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],
   "outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"balanceOf","stateMutability":"view",
   "inputs":[{"name":"account","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"allowance","stateMutability":"view",
   "inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"approve","stateMutability":"nonpayable",
   "inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"transferFrom","stateMutability":"nonpayable",
   "inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],
   "outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint8"}]},
  {"type":"function","name":"symbol","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},
  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]}
]`

var (
	factoryABI = mustABI("smart-invoice-factory", factoryABIJSON)
	escrowABI  = mustABI("smart-invoice-escrow", escrowABIJSON)
	erc20ABI   = mustABI("erc20", erc20ABIJSON)
)

func mustABI(label, jsonStr string) *web3.ABI {
	a, err := web3.ParseABIJSON([]byte(jsonStr))
	if err != nil {
		panic(fmt.Sprintf("settlement: bundled ABI %s: %v", label, err))
	}
	return a
}

// SmartInvoiceFactoryABI returns the bundled factory ABI.
func SmartInvoiceFactoryABI() *web3.ABI { return factoryABI }

// SmartInvoiceEscrowABI returns the bundled per-session escrow ABI.
func SmartInvoiceEscrowABI() *web3.ABI { return escrowABI }

// ERC20ABI returns the bundled token ABI.
func ERC20ABI() *web3.ABI { return erc20ABI }
