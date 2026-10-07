// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

// RhizomeEscrow — the v0.16.0 graduated settlement contract for the open
// agent work market (Track 127). Session-keyed single deployment: one
// contract carries every session, keyed by a bytes32 session id the
// buyer derives off-chain (keccak256(correlationID)).
//
// Lifecycle (design surface — docs/design/v0.16.0-sprint.md):
//
//   open(sessionId, seller, token, amount, taskHash, disputeWindow)
//     buyer funds via transferFrom (approve first); session = Open.
//   release(sessionId, amount)
//     buyer-only partial release while the window runs — pays seller
//     immediately (drawdown-ready; Track 128 rides this).
//   dispute(sessionId, evidenceHash)
//     buyer OR seller while the window runs — freezes the session and
//     emits ERC-1497 Evidence; the session's arbiter must resolve.
//   resolve(sessionId, buyerAward, sellerAward, rulingHash)
//     arbiter-only while Locked — awards must sum to the remaining
//     balance; pays both sides and closes.
//   claim(sessionId)
//     seller-only after the dispute deadline — the native seller-pull
//     Smart Invoice lacked: a buyer that neither disputes nor releases
//     does not strand the provider.
//   withdraw(sessionId)
//     buyer-only after deadline + CLAIM_GRACE — the clawback that frees
//     the remainder when the seller abandons the session too.
//   submitEvidence(sessionId, uri)
//     either party while Locked — ERC-1497 evidence emission for the
//     arbiter (and the Track 129 ERC-792 adapter path).
//
// Arbiter is per-session (escrow_arbiter module field): a plain EOA or a
// contract (the Track 129 Kleros adapter is a contract arbiter whose
// ruling calls back into resolve()).
interface IERC20Minimal {
    function transferFrom(
        address from, address to, uint256 amount
    ) external returns (bool);
    function transfer(address to, uint256 amount) external returns (bool);
    function balanceOf(address account) external view returns (uint256);
}

contract RhizomeEscrow {
    // CLAIM_GRACE gives the seller exclusive claim rights after the
    // dispute deadline lapses; only after deadline + CLAIM_GRACE may the
    // buyer withdraw an abandoned remainder. 14 days is comfortably past
    // any reasonable seller-claim latency on testnet/L2.
    uint64 public constant CLAIM_GRACE = 14 days;
    // Window bounds: long enough to cover a real session, short enough
    // that a fat-fingered duration can't strand funds for years.
    uint64 public constant MIN_DISPUTE_WINDOW = 1 hours;
    uint64 public constant MAX_DISPUTE_WINDOW = 365 days;

    enum Status { None, Open, Locked, Resolved, Closed }

    struct Session {
        address buyer;
        address seller;
        address arbiter;
        address token;
        uint128 amount;    // budget deposited at open
        uint128 released;  // cumulative released to seller
        uint64  deadline;  // dispute window end (unix)
        Status  status;
        bytes32 taskHash;  // work commitment — dispute evidence anchor
    }

    mapping(bytes32 => Session) public sessions;

    // Market lifecycle events — sessionId indexed for eth_getLogs
    // watches (the module filters contract-address + sessionId topic).
    event Opened(
        bytes32 indexed sessionId,
        address indexed buyer,
        address indexed seller,
        address token,
        uint128 amount,
        uint64 deadline,
        bytes32 taskHash);
    event Released(bytes32 indexed sessionId, uint128 amount, uint128 released);
    event Disputed(bytes32 indexed sessionId, address indexed by, bytes32 evidenceHash);
    event Resolved(
        bytes32 indexed sessionId,
        uint128 buyerAward,
        uint128 sellerAward,
        bytes32 rulingHash);
    event Claimed(bytes32 indexed sessionId, uint128 amount);
    event Withdrawn(bytes32 indexed sessionId, uint128 amount);

    // ERC-1497 evidence hooks — the standard event shapes, with the
    // session id as the evidence group and the arbiter in the arbitrator
    // slot (a Track 129 ERC-792 adapter contract presents as `arbiter`
    // without changing these events).
    event MetaEvidence(uint256 indexed metaEvidenceID, string evidence);
    event Evidence(
        address indexed arbitrator,
        uint256 indexed evidenceGroupID,
        address indexed submitter,
        string evidence);
    event Ruling(bytes32 indexed sessionId, uint128 buyerAward, uint128 sellerAward);

    uint256 private _locked;

    modifier nonReentrant() {
        require(_locked == 0, "reentrant");
        _locked = 1;
        _;
        _locked = 0;
    }

    /// open funds a session escrow. sessionId must be unused (callers
    /// derive it as keccak256(correlationID)); disputeWindow is seconds
    /// from now to the dispute deadline.
    function open(
        bytes32 sessionId,
        address seller,
        address token,
        uint128 amount,
        bytes32 taskHash,
        uint64 disputeWindow,
        address arbiter
    ) external nonReentrant {
        require(sessionId != bytes32(0), "session id required");
        require(sessions[sessionId].status == Status.None, "session exists");
        require(seller != address(0) && seller != msg.sender, "bad seller");
        require(token != address(0), "bad token");
        require(arbiter != address(0), "bad arbiter");
        require(amount > 0, "amount required");
        require(
            disputeWindow >= MIN_DISPUTE_WINDOW &&
            disputeWindow <= MAX_DISPUTE_WINDOW,
            "window out of range");

        uint64 deadline = uint64(block.timestamp) + disputeWindow;
        sessions[sessionId] = Session({
            buyer: msg.sender,
            seller: seller,
            arbiter: arbiter,
            token: token,
            amount: amount,
            released: 0,
            deadline: deadline,
            status: Status.Open,
            taskHash: taskHash
        });

        // Checks-effects before the pull: the state is committed even if
        // the transfer reverts (the revert unwinds it anyway).
        require(
            IERC20Minimal(token).transferFrom(
                msg.sender, address(this), amount),
            "funding transfer failed");

        emit Opened(
            sessionId, msg.sender, seller, token, amount, deadline, taskHash);
        emit MetaEvidence(uint256(sessionId), "rhizome market session");
    }

    /// release pays `amount` of the remaining escrow to the seller —
    /// buyer-only, pre-deadline, unlocked. Partial releases are the
    /// drawdown primitive; releasing the remainder is the settle.
    function release(
        bytes32 sessionId, uint128 amount
    ) external nonReentrant {
        Session storage s = sessions[sessionId];
        require(s.status == Status.Open, "not open");
        require(msg.sender == s.buyer, "buyer only");
        require(block.timestamp <= s.deadline, "window lapsed");
        require(amount > 0 && s.released + amount <= s.amount, "bad amount");

        s.released += amount;
        require(IERC20Minimal(s.token).transfer(s.seller, amount),
            "release transfer failed");
        emit Released(sessionId, amount, s.released);
    }

    /// dispute freezes the session for arbitration — either party,
    /// pre-deadline. evidenceHash anchors the off-chain evidence bundle
    /// (the _rhizome.receipt JSON + terms); ERC-1497 Evidence follows via
    /// submitEvidence.
    function dispute(
        bytes32 sessionId, bytes32 evidenceHash
    ) external nonReentrant {
        Session storage s = sessions[sessionId];
        require(s.status == Status.Open, "not open");
        require(
            msg.sender == s.buyer || msg.sender == s.seller,
            "party only");
        require(block.timestamp <= s.deadline, "window lapsed");

        s.status = Status.Locked;
        emit Disputed(sessionId, msg.sender, evidenceHash);
    }

    /// submitEvidence emits ERC-1497-shaped evidence for the arbiter —
    /// either party while Locked.
    function submitEvidence(
        bytes32 sessionId, string calldata evidence
    ) external {
        Session storage s = sessions[sessionId];
        require(s.status == Status.Locked, "not locked");
        require(
            msg.sender == s.buyer || msg.sender == s.seller,
            "party only");
        emit Evidence(s.arbiter, uint256(sessionId), msg.sender, evidence);
    }

    /// resolve splits the remaining balance between buyer and seller —
    /// arbiter-only, Locked only. Awards must consume the balance exactly
    /// (no dust, no fee skimming — a fee-taking arbiter is a Track 129
    /// adapter contract, not a parameter here).
    function resolve(
        bytes32 sessionId,
        uint128 buyerAward,
        uint128 sellerAward,
        bytes32 rulingHash
    ) external nonReentrant {
        Session storage s = sessions[sessionId];
        require(s.status == Status.Locked, "not locked");
        require(msg.sender == s.arbiter, "arbiter only");
        uint256 remaining = uint256(s.amount) - uint256(s.released);
        require(
            uint256(buyerAward) + uint256(sellerAward) == remaining,
            "awards must equal balance");

        s.status = Status.Resolved;
        emit Resolved(sessionId, buyerAward, sellerAward, rulingHash);
        emit Ruling(sessionId, buyerAward, sellerAward);
        if (buyerAward > 0) {
            require(IERC20Minimal(s.token).transfer(s.buyer, buyerAward),
                "buyer award transfer failed");
        }
        if (sellerAward > 0) {
            require(IERC20Minimal(s.token).transfer(s.seller, sellerAward),
                "seller award transfer failed");
        }
    }

    /// claim is the native seller-pull: after the dispute deadline, an
    /// undisputed session's remaining balance belongs to the seller.
    /// The claim gap this fixes is documented in
    /// docs/design/escrow-survey.md.
    function claim(bytes32 sessionId) external nonReentrant {
        Session storage s = sessions[sessionId];
        require(s.status == Status.Open, "not open");
        require(msg.sender == s.seller, "seller only");
        require(block.timestamp > s.deadline, "window still running");

        uint256 remaining = uint256(s.amount) - uint256(s.released);
        s.status = Status.Closed;
        emit Claimed(sessionId, uint128(remaining));
        if (remaining > 0) {
            require(IERC20Minimal(s.token).transfer(s.seller, remaining),
                "claim transfer failed");
        }
    }

    /// withdraw is the buyer's abandonment clawback — after the dispute
    /// deadline AND the seller's claim grace both lapse, the remainder
    /// returns to the buyer. A session nobody settles can't strand funds.
    function withdraw(bytes32 sessionId) external nonReentrant {
        Session storage s = sessions[sessionId];
        require(s.status == Status.Open, "not open");
        require(msg.sender == s.buyer, "buyer only");
        require(
            block.timestamp > s.deadline + CLAIM_GRACE,
            "claim grace still running");

        uint256 remaining = uint256(s.amount) - uint256(s.released);
        s.status = Status.Closed;
        emit Withdrawn(sessionId, uint128(remaining));
        if (remaining > 0) {
            require(IERC20Minimal(s.token).transfer(s.buyer, remaining),
                "withdraw transfer failed");
        }
    }

    /// sessionOf is a compact view for VerifyLock-style reads.
    function sessionOf(bytes32 sessionId) external view returns (
        address buyer, address seller, address arbiter, address token,
        uint128 amount, uint128 released, uint64 deadline,
        uint8 status, bytes32 taskHash
    ) {
        Session storage s = sessions[sessionId];
        return (s.buyer, s.seller, s.arbiter, s.token, s.amount,
            s.released, s.deadline, uint8(s.status), s.taskHash);
    }
}
