package settlement

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// FakeChain Kleros model — contracts/RhizomeKlerosAdapter.sol + a
// minimal ERC-792 arbitrator. The adapter occupies the session's
// arbiter slot; createDispute mints a dispute id, and the arbitrator's
// rule() callback resolves the session (ruling 1=buyer, 2=seller,
// 0=even split — odd wei to the buyer, mirroring the .sol).
//
// The fake does not model ether balances: arbitrationCost is quoted and
// required but the wei itself isn't ledgered — fee accounting is an
// on-chain check (Track 132), not a fixture concern.
//
// DeployKleros wires the pair; SetArbiterRuling presets the auto-ruling
// delivered synchronously on createDispute (the fake's compressed
// juror round). A zero/disabled preset leaves the dispute pending for
// manual rule() delivery.

// DeployKleros registers the adapter (occupies session arbiter slots)
// and its arbitrator for this chain — the fixture analogue of deploying
// both contracts.
func (fc *FakeChain) DeployKleros(adapter, arbitrator string, cost *big.Int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.klerosAdapter = normAddr(adapter)
	fc.klerosArbitrator = normAddr(arbitrator)
	fc.klerosCost = new(big.Int).Set(cost)
}

// SetArbiterRuling presets the ruling the fake arbitrator auto-delivers
// on the next createDispute (its compressed juror round). -1 disables
// auto-delivery — the dispute stays pending for a manual rule() call.
func (fc *FakeChain) SetArbiterRuling(ruling int64) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.klerosRuling = ruling
}

// klerosAdapterView answers eth_call reads on the adapter contract.
func (fc *FakeChain) klerosAdapterView(data []byte) (any, *rpcError) {
	s := hex.EncodeToString(data[:4])
	switch s {
	case hex.EncodeToString(sel("arbitrationCost", klerosAdapterABI, 0)):
		return encRet([]string{"uint256"}, []any{fc.klerosCost.String()})
	case hex.EncodeToString(sel("arbitrator", klerosAdapterABI, 0)):
		return encRet([]string{"address"}, []any{fc.klerosArbitrator})
	case hex.EncodeToString(sel("disputeOfSession", klerosAdapterABI, 1)):
		args, rerr := fc.decodeArgs([]string{"bytes32"}, data)
		if rerr != nil {
			return nil, rerr
		}
		id := fc.klerosDisputesBySession[normSessionID(mustStr(args[0]))]
		return encRet([]string{"uint256"},
			[]any{new(big.Int).SetUint64(id).String()})
	}
	return nil, rpcErr(-32601, "fakechain: unknown adapter selector")
}

// arbitratorView answers eth_call reads on the arbitrator contract.
func (fc *FakeChain) arbitratorView(data []byte) (any, *rpcError) {
	s := hex.EncodeToString(data[:4])
	if s == hex.EncodeToString(sel("arbitrationCost", arbitratorABI, 1)) {
		return encRet([]string{"uint256"}, []any{fc.klerosCost.String()})
	}
	return nil, rpcErr(-32601, "fakechain: unknown arbitrator selector")
}

// klerosAdapterTx applies a transaction to the adapter contract.
func (fc *FakeChain) klerosAdapterTx(
	from string, data []byte,
) ([]*fakeLog, *rpcError) {
	s := hex.EncodeToString(data[:4])
	switch s {
	case hex.EncodeToString(sel("createDispute", klerosAdapterABI, 1)):
		return fc.adapterCreateDispute(from, data)
	case hex.EncodeToString(sel("rule", klerosAdapterABI, 2)):
		return fc.adapterRule(from, data)
	}
	return nil, rpcErr(-32601, "fakechain: unsupported adapter tx selector")
}

// adapterCreateDispute mirrors the adapter contract: the session must
// exist, name this adapter as arbiter, and still hold a remainder; the
// dispute id registers both directions; a preset ruling auto-delivers
// the arbitrator's rule() in the same block.
func (fc *FakeChain) adapterCreateDispute(
	_ string, data []byte,
) ([]*fakeLog, *rpcError) {
	args, rerr := fc.decodeArgs([]string{"bytes32"}, data)
	if rerr != nil {
		return nil, rerr
	}
	sid := normSessionID(mustStr(args[0]))
	e := fc.gradSessions[sid]
	if e == nil {
		return nil, revert("no session")
	}
	if e.arbiter != fc.klerosAdapter {
		return nil, revert("not this adapter's session")
	}
	if new(big.Int).Sub(e.amount, e.released).Sign() <= 0 {
		return nil, revert("nothing to arbitrate")
	}
	// Locked only — a ruling on an un-disputed session would revert at
	// escrow.resolve forever; fail before minting (mirrors the .sol).
	if e.status != graduatedStatusLocked {
		return nil, revert("session not locked")
	}
	if fc.klerosDisputesBySession[sid] != 0 {
		return nil, revert("already escalated")
	}
	fc.klerosSeq++
	disputeID := fc.klerosSeq
	fc.klerosDisputes[disputeID] = sid
	fc.klerosDisputesBySession[sid] = disputeID
	logs := []*fakeLog{{
		address: fc.klerosAdapter,
		topics: []string{
			topicOf("Dispute(address,uint256,uint256,uint256)"),
			topicAddr(fc.klerosArbitrator), topicUint(disputeID),
		},
		data: "0x" + hex.EncodeToString(concat(
			wordBig(new(big.Int).SetUint64(sidUint(sid))),
			wordBig(new(big.Int).SetUint64(sidUint(sid))))),
	}}
	if fc.klerosRuling >= 0 {
		ruling := uint64(fc.klerosRuling)
		if l, rerr := fc.deliverRule(disputeID, ruling); rerr != nil {
			return nil, rerr
		} else {
			logs = append(logs, l...)
		}
	}
	return logs, nil
}

// adapterRule is the arbitrator's IArbitrable callback — arbitrator-only.
func (fc *FakeChain) adapterRule(
	from string, data []byte,
) ([]*fakeLog, *rpcError) {
	if from != fc.klerosArbitrator {
		return nil, revert("arbitrator only")
	}
	args, rerr := fc.decodeArgs([]string{"uint256", "uint256"}, data)
	if rerr != nil {
		return nil, rerr
	}
	return fc.deliverRule(mustBig(args[0]).Uint64(), mustBig(args[1]).Uint64())
}

// deliverRule maps the ruling onto escrow.resolve semantics: awards
// consume the session's remaining balance exactly.
func (fc *FakeChain) deliverRule(
	disputeID, ruling uint64,
) ([]*fakeLog, *rpcError) {
	sid, ok := fc.klerosDisputes[disputeID]
	if !ok {
		return nil, revert("unknown dispute")
	}
	e := fc.gradSessions[sid]
	if e == nil || e.status != graduatedStatusLocked {
		return nil, revert("not locked")
	}
	remaining := new(big.Int).Sub(e.amount, e.released)
	var buyerAward, sellerAward *big.Int
	switch ruling {
	case 1:
		buyerAward, sellerAward = new(big.Int).Set(remaining), new(big.Int)
	case 2:
		buyerAward, sellerAward = new(big.Int), new(big.Int).Set(remaining)
	default:
		sellerAward = new(big.Int).Div(remaining, big.NewInt(2))
		buyerAward = new(big.Int).Sub(remaining, sellerAward)
	}
	e.status = graduatedStatusResolved
	if sellerAward.Sign() > 0 {
		fc.tokenMove(e.token, normAddr(fc.factory), e.seller, sellerAward)
	}
	if buyerAward.Sign() > 0 {
		fc.tokenMove(e.token, normAddr(fc.factory), e.buyer, buyerAward)
	}
	var rulingSeed [16]byte // disputeID (uint64) | ruling (uint64) BE
	binary.BigEndian.PutUint64(rulingSeed[:8], disputeID)
	binary.BigEndian.PutUint64(rulingSeed[8:], ruling)
	rulingHash := web3.Keccak256(rulingSeed[:])
	var rh [32]byte
	copy(rh[:], rulingHash)
	return []*fakeLog{
		{
			address: fc.klerosAdapter,
			topics: []string{
				topicOf("Ruling(address,uint256,uint256)"),
				topicAddr(fc.klerosArbitrator), topicUint(disputeID),
			},
			data: "0x" + hex.EncodeToString(wordBig(new(big.Int).SetUint64(ruling))),
		},
		{
			address: normAddr(fc.factory),
			topics: []string{
				topicOf("Resolved(bytes32,uint128,uint128,bytes32)"),
				topicBytes32Str(sid),
			},
			data: "0x" + hex.EncodeToString(
				concat(wordBig(buyerAward), wordBig(sellerAward), wordBytes32(rh))),
		},
	}, nil
}

// sidUint folds a session-id hex into a uint for the Dispute event's
// metaEvidenceID/evidenceGroupID fields (the adapter passes
// uint256(sessionId) — a fold keeps the field small for assertions).
func sidUint(sid string) uint64 {
	var v uint64
	for i := len(sid); i > 0 && len(sid)-i < 8; i-- {
		v = v<<8 | uint64(sid[i-1])
	}
	return v
}
