package settlement

import "github.com/stpinkie/rhizome/pkg/web3"

// Bundled ABIs for the Track 129 arbitration path: the
// RhizomeKlerosAdapter (contracts/RhizomeKlerosAdapter.sol) and the
// minimal IArbitrator surface it forwards to (KlerosLiquid's
// arbitrationCost/createDispute). Hand-authored like the escrow bundle —
// keep byte-identical to the .sol surface or FakeGraduated diverges.
const klerosAdapterABIJSON = `[
  {"type":"function","name":"arbitrationCost","stateMutability":"view",
   "inputs":[],"outputs":[{"name":"cost","type":"uint256"}]},
  {"type":"function","name":"createDispute","stateMutability":"payable",
   "inputs":[{"name":"sessionId","type":"bytes32"}],
   "outputs":[{"name":"disputeID","type":"uint256"}]},
  {"type":"function","name":"rule","stateMutability":"nonpayable",
   "inputs":[{"name":"disputeID","type":"uint256"},{"name":"ruling","type":"uint256"}],"outputs":[]},
  {"type":"function","name":"disputeOfSession","stateMutability":"view",
   "inputs":[{"name":"sessionId","type":"bytes32"}],
   "outputs":[{"name":"disputeID","type":"uint256"}]},
  {"type":"function","name":"arbitrator","stateMutability":"view",
   "inputs":[],"outputs":[{"name":"addr","type":"address"}]},
  {"type":"event","name":"Dispute",
   "inputs":[{"name":"_arbitrator","type":"address","indexed":true},{"name":"_disputeID","type":"uint256","indexed":true},{"name":"_metaEvidenceID","type":"uint256","indexed":false},{"name":"_evidenceGroupID","type":"uint256","indexed":false}]},
  {"type":"event","name":"Ruling",
   "inputs":[{"name":"_arbitrator","type":"address","indexed":true},{"name":"_disputeID","type":"uint256","indexed":true},{"name":"_ruling","type":"uint256","indexed":false}]}
]`

const arbitratorABIJSON = `[
  {"type":"function","name":"arbitrationCost","stateMutability":"view",
   "inputs":[{"name":"extraData","type":"bytes"}],
   "outputs":[{"name":"cost","type":"uint256"}]},
  {"type":"function","name":"createDispute","stateMutability":"payable",
   "inputs":[{"name":"choices","type":"uint256"},{"name":"extraData","type":"bytes"}],
   "outputs":[{"name":"disputeID","type":"uint256"}]}
]`

var (
	klerosAdapterABI = mustABI("rhizome-kleros-adapter", klerosAdapterABIJSON)
	arbitratorABI    = mustABI("erc792-arbitrator", arbitratorABIJSON)
)

// KlerosAdapterABI returns the bundled adapter ABI.
func KlerosAdapterABI() *web3.ABI { return klerosAdapterABI }

// ArbitratorABI returns the minimal IArbitrator ABI.
func ArbitratorABI() *web3.ABI { return arbitratorABI }
