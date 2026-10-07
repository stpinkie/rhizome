// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

// RhizomeKlerosAdapter — the Track 129 pluggable arbiter. It occupies a
// session's `arbiter` slot on RhizomeEscrow and translates an ERC-792
// arbitrator's ruling (KlerosLiquid on Sepolia) into the escrow's
// resolve() split. The escrow contract is untouched — the adapter is
// the extension, not a redeploy.
//
// Flow:
//   1. Buyer (or seller) calls escrow.dispute(sessionId, evidenceHash)
//      — the session Locks. Both parties submitEvidence while Locked.
//   2. The disputing party calls createDispute{value: arbitrationCost}
//      — the fee forwards to the ERC-792 arbitrator, which mints a
//      Kleros dispute id and (on-chain, after the juror round) calls
//      back rule(disputeID, ruling).
//   3. rule() reads the session's remaining balance and calls
//      escrow.resolve(sessionId, buyerAward, sellerAward, rulingHash).
//
// Ruling convention (2 choices, matching Kleros binary disputes):
//   ruling 1 = buyer wins   — remaining balance returns to the buyer
//   ruling 2 = seller wins  — remaining balance pays the seller
//   ruling 0 = refused      — split even (odd wei lands with the buyer,
//                             the party least trusted by construction)
//
// Fee model (documented in docs/operations/dispute-desk.md): the party
// escalating pays arbitrationCost at createDispute; Kleros appeal
// rounds price each side separately on the arbitrator, outside this
// adapter's surface.
//
// The arbitrator + extraData are constructor-pinned — one adapter
// deployment per (escrow, arbitrator, subcourt) tuple. Sessions bind
// the adapter as `arbiter` at open().

interface IRhizomeEscrow {
    function sessions(bytes32 sessionId) external view returns (
        address buyer, address seller, address arbiter, address token,
        uint128 amount, uint128 released, uint64 deadline,
        uint8 status, bytes32 taskHash, bool drawdown);
    function resolve(
        bytes32 sessionId,
        uint128 buyerAward,
        uint128 sellerAward,
        bytes32 rulingHash
    ) external;
}

interface IArbitrator {
    function arbitrationCost(
        bytes calldata extraData
    ) external view returns (uint256 cost);
    function createDispute(
        uint256 choices,
        bytes calldata extraData
    ) external payable returns (uint256 disputeID);
}

contract RhizomeKlerosAdapter {
    IRhizomeEscrow public immutable escrow;
    IArbitrator public immutable arbitrator;
    bytes public extraData; // subcourt + juror count, arbitrator-encoded

    // ERC-792 IArbitrable events — the shapes indexers and the
    // arbitrator's own dispute view expect.
    event Dispute(
        IArbitrator indexed _arbitrator,
        uint256 indexed _disputeID,
        uint256 _metaEvidenceID,
        uint256 _evidenceGroupID);
    event Ruling(
        IArbitrator indexed _arbitrator,
        uint256 indexed _disputeID,
        uint256 _ruling);

    mapping(uint256 => bytes32) public sessionOfDispute;
    mapping(bytes32 => uint256) public disputeOfSession;
    uint256 public constant CHOICES = 2;

    modifier onlyArbitrator() {
        require(msg.sender == address(arbitrator), "arbitrator only");
        _;
    }

    constructor(address _escrow, address _arbitrator, bytes memory _extraData) {
        require(_escrow != address(0) && _arbitrator != address(0), "bad binding");
        escrow = IRhizomeEscrow(_escrow);
        arbitrator = IArbitrator(_arbitrator);
        extraData = _extraData;
    }

    /// arbitrationCost quotes the escalation fee — the payable amount
    /// createDispute requires (KlerosLiquid prices per round).
    function arbitrationCost() external view returns (uint256) {
        return arbitrator.arbitrationCost(extraData);
    }

    /// createDispute escalates a Locked session to the arbitrator. The
    /// caller pays the arbitration fee — the design assigns it to the
    /// party escalating (the loser-pays-appeal-fees model lives on the
    /// arbitrator, not here).
    function createDispute(
        bytes32 sessionId
    ) external payable returns (uint256 disputeID) {
        require(
            msg.value >= arbitrator.arbitrationCost(extraData),
            "fee under-paid");
        require(disputeOfSession[sessionId] == 0, "already escalated");
        (, , address arbiter, , uint128 amount, uint128 released,
            , uint8 status, , ) = escrow.sessions(sessionId);
        require(arbiter == address(this), "not this adapter's session");
        require(amount > released, "nothing to arbitrate");
        // Locked only — a ruling on an un-disputed session would revert
        // at escrow.resolve forever (it requires status Locked), and the
        // arbitration fee is unrecoverable. Fail before minting.
        require(status == 2, "session not locked");

        disputeID = arbitrator.createDispute{value: msg.value}(
            CHOICES, extraData);
        sessionOfDispute[disputeID] = sessionId;
        disputeOfSession[sessionId] = disputeID;
        emit Dispute(
            arbitrator, disputeID, uint256(sessionId), uint256(sessionId));
    }

    /// rule is the IArbitrable callback — arbitrator-only. Maps the
    /// ruling onto escrow.resolve: awards consume the remaining balance
    /// exactly (the escrow requires it).
    function rule(
        uint256 disputeID,
        uint256 ruling
    ) external onlyArbitrator {
        bytes32 sessionId = sessionOfDispute[disputeID];
        require(sessionId != bytes32(0), "unknown dispute");
        (, , , , uint128 amount, uint128 released, , , , ) =
            escrow.sessions(sessionId);
        uint128 remaining = amount - released;

        uint128 buyerAward;
        uint128 sellerAward;
        if (ruling == 1) {
            buyerAward = remaining;
        } else if (ruling == 2) {
            sellerAward = remaining;
        } else {
            // Refused-to-arbitrate: even split, odd wei to the buyer.
            sellerAward = remaining / 2;
            buyerAward = remaining - sellerAward;
        }
        emit Ruling(arbitrator, disputeID, ruling);
        escrow.resolve(
            sessionId, buyerAward, sellerAward,
            keccak256(abi.encodePacked(disputeID, ruling)));
    }
}
