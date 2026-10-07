package settlement

import "github.com/stpinkie/rhizome/pkg/web3"

// Bundled ABI for the graduated RhizomeEscrow (contracts/RhizomeEscrow.sol,
// v0.16.0 Track 127) — session-keyed single deployment, amount-based
// partial releases, native seller claim, ERC-1497 evidence hooks. Hand-
// authored like the Smart Invoice bundle: keep it byte-identical to the
// .sol surface or the FakeGraduated model and the real chain diverge.
const graduatedABIJSON = `[
  {"type":"function","name":"open","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"},{"name":"seller","type":"address"},{"name":"token","type":"address"},{"name":"amount","type":"uint128"},{"name":"taskHash","type":"bytes32"},{"name":"disputeWindow","type":"uint64"},{"name":"arbiter","type":"address"},{"name":"drawdown","type":"bool"}],"outputs":[]},
  {"type":"function","name":"release","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"},{"name":"amount","type":"uint128"}],"outputs":[]},
  {"type":"function","name":"dispute","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"},{"name":"evidenceHash","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"resolve","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"},{"name":"buyerAward","type":"uint128"},{"name":"sellerAward","type":"uint128"},{"name":"rulingHash","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"claim","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"withdraw","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"submitEvidence","stateMutability":"nonpayable",
   "inputs":[{"name":"sessionId","type":"bytes32"},{"name":"evidence","type":"string"}],"outputs":[]},
  {"type":"function","name":"sessionOf","stateMutability":"view",
   "inputs":[{"name":"sessionId","type":"bytes32"}],
   "outputs":[{"name":"buyer","type":"address"},{"name":"seller","type":"address"},{"name":"arbiter","type":"address"},{"name":"token","type":"address"},{"name":"amount","type":"uint128"},{"name":"released","type":"uint128"},{"name":"deadline","type":"uint64"},{"name":"status","type":"uint8"},{"name":"taskHash","type":"bytes32"},{"name":"drawdown","type":"bool"}]},
  {"type":"function","name":"sessions","stateMutability":"view",
   "inputs":[{"name":"sessionId","type":"bytes32"}],
   "outputs":[{"name":"buyer","type":"address"},{"name":"seller","type":"address"},{"name":"arbiter","type":"address"},{"name":"token","type":"address"},{"name":"amount","type":"uint128"},{"name":"released","type":"uint128"},{"name":"deadline","type":"uint64"},{"name":"status","type":"uint8"},{"name":"taskHash","type":"bytes32"},{"name":"drawdown","type":"bool"}]},
  {"type":"event","name":"Opened",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"buyer","type":"address","indexed":true},{"name":"seller","type":"address","indexed":true},{"name":"token","type":"address","indexed":false},{"name":"amount","type":"uint128","indexed":false},{"name":"deadline","type":"uint64","indexed":false},{"name":"taskHash","type":"bytes32","indexed":false}]},
  {"type":"event","name":"Released",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"amount","type":"uint128","indexed":false},{"name":"released","type":"uint128","indexed":false}]},
  {"type":"event","name":"Disputed",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"by","type":"address","indexed":true},{"name":"evidenceHash","type":"bytes32","indexed":false}]},
  {"type":"event","name":"Resolved",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"buyerAward","type":"uint128","indexed":false},{"name":"sellerAward","type":"uint128","indexed":false},{"name":"rulingHash","type":"bytes32","indexed":false}]},
  {"type":"event","name":"Claimed",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"amount","type":"uint128","indexed":false}]},
  {"type":"event","name":"Withdrawn",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"amount","type":"uint128","indexed":false}]},
  {"type":"event","name":"MetaEvidence",
   "inputs":[{"name":"metaEvidenceID","type":"uint256","indexed":true},{"name":"evidence","type":"string","indexed":false}]},
  {"type":"event","name":"Evidence",
   "inputs":[{"name":"arbitrator","type":"address","indexed":true},{"name":"evidenceGroupID","type":"uint256","indexed":true},{"name":"submitter","type":"address","indexed":true},{"name":"evidence","type":"string","indexed":false}]},
  {"type":"event","name":"Ruling",
   "inputs":[{"name":"sessionId","type":"bytes32","indexed":true},{"name":"buyerAward","type":"uint128","indexed":false},{"name":"sellerAward","type":"uint128","indexed":false}]}
]`

var graduatedABI = mustABI("rhizome-escrow", graduatedABIJSON)

// GraduatedEscrowABI returns the bundled RhizomeEscrow ABI.
func GraduatedEscrowABI() *web3.ABI { return graduatedABI }

// GraduatedSessionID derives the on-chain session key for a correlation
// id — keccak256(correlationID), the same derivation CorrelationSalt uses
// for Smart Invoice salts. The wire session_id under the graduated rail
// is this bytes32 hex, not an escrow clone address.
func GraduatedSessionID(correlationID string) [32]byte {
	return CorrelationSalt(correlationID)
}

// Graduated session status values — uint8(Status) in the ABI order.
const (
	graduatedStatusNone     = 0
	graduatedStatusOpen     = 1
	graduatedStatusLocked   = 2
	graduatedStatusResolved = 3
	graduatedStatusClosed   = 4
)
