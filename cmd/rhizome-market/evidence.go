package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// ERC-1497 evidence for the graduated rail (Track 129). The bundle the
// arbiter reads is the seller's signed _rhizome.receipt plus a terms
// hash committing to the on-chain session facts — keccak256 over the
// pipe-joined session id, token, amount, and task hash. The arbiter
// can verify the hash against sessions(sessionId) on-chain without
// trusting either party's presentation.
//
// The bundle is canonical JSON (stable field order via struct marshal)
// so both parties hash the same bytes the dispute() call commits.

// evidenceBundle is the submission payload for submitEvidence().
type evidenceBundle struct {
	SessionID string          `json:"session_id"`
	TermsHash string          `json:"terms_hash"`
	Receipt   json.RawMessage `json:"receipt,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

// termsHash commits to the session's on-chain facts: keccak256 over
// sessionID|token|amount|taskHash — the fields an arbiter verifies
// against sessions() before weighing the receipt.
func termsHash(sessionID, token, amount, taskHash string) string {
	return "0x" + hex.EncodeToString(web3.Keccak256([]byte(
		strings.ToLower(sessionID)+"|"+
			strings.ToLower(token)+"|"+amount+"|"+taskHash)))
}

// buildEvidence marshals the dispute bundle and returns it with the
// keccak256 dispute commits as detailsHash. A missing receipt isn't
// fatal — the bundle still carries the terms hash (the arbiter weighs
// an absent receipt against the seller).
func buildEvidence(p *purchase, reason string) (jsonStr string, detailsHash [32]byte) {
	bundle := evidenceBundle{
		SessionID: p.SessionID,
		TermsHash: termsHash(p.SessionID, p.Terms.Token, p.Terms.Amount, p.TaskHash),
		Reason:    reason,
	}
	if p.Receipt != nil {
		if raw, err := json.Marshal(p.Receipt); err == nil {
			bundle.Receipt = raw
		}
	}
	data, err := json.Marshal(&bundle)
	if err != nil {
		return "", detailsHash
	}
	copy(detailsHash[:], web3.Keccak256(data))
	return string(data), detailsHash
}

// evidenceJSON pretty-prints the bundle for the operator — the same
// bytes that went on-chain, readable.
func evidenceJSON(p *purchase, reason string) (string, error) {
	js, _ := buildEvidence(p, reason)
	if js == "" {
		return "", fmt.Errorf("evidence bundle failed to marshal")
	}
	return js, nil
}
