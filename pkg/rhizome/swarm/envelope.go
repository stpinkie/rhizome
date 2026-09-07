// Package swarm implements swarm collaboration: named groups of trusted
// mesh peers that coordinate over the /rhizome/swarm/1.0.0 stream protocol.
// Membership is always scoped to the existing mesh trust set — a peer must
// be trusted before it can join or appear in a roster.
package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolID is the libp2p protocol for swarm coordination messages.
const ProtocolID = protocol.ID("/rhizome/swarm/1.0.0")

// MsgType identifies the swarm operation an envelope carries.
type MsgType string

const (
	// MsgJoin announces that the sender joined the swarm.
	MsgJoin MsgType = "join"
	// MsgJoinAck answers a join; the payload reports whether the responder is
	// itself a member and gossip's the responder's known member list.
	MsgJoinAck MsgType = "join_ack"
	// MsgLeave announces that the sender left the swarm.
	MsgLeave MsgType = "leave"
	// MsgPing is a periodic presence heartbeat carrying a capability digest.
	MsgPing MsgType = "ping"
	// MsgQuery asks a peer which swarms it has joined and its known members.
	MsgQuery MsgType = "query"
	// MsgQueryResp answers a query.
	MsgQueryResp MsgType = "query_resp"
	// MsgBroadcast carries an application-level swarm message (task offers,
	// claims, assignments, coordination, orchestrator envelopes) delivered to
	// local subscribers. It is used both for swarm-wide broadcast fan-out and
	// for point-to-point queue messages (e.g. a claim sent to the offerer);
	// the transport delivers it to local subscribers in both cases.
	MsgBroadcast MsgType = "msg"
)

// swarmIDPattern bounds swarm ids to short, safe identifiers usable in file
// paths and protocol strings.
var swarmIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ValidSwarmID reports whether id is an acceptable swarm identifier.
func ValidSwarmID(id string) bool {
	return swarmIDPattern.MatchString(id)
}

// Envelope is a single signed swarm message. The signature covers the
// canonical JSON encoding of all fields with Signature cleared, proving the
// envelope was issued by the From peer. Nonce and Timestamp feed the replay
// guard.
type Envelope struct {
	SwarmID   string          `json:"swarm_id"`
	Type      MsgType         `json:"type"`
	From      string          `json:"from,omitempty"`
	Nonce     string          `json:"nonce,omitempty"`
	Timestamp int64           `json:"timestamp,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Signature []byte          `json:"signature,omitempty"`
}

// joinAckPayload is carried by MsgJoinAck responses.
type joinAckPayload struct {
	// Member reports whether the responder is itself a swarm member.
	Member bool `json:"member"`
	// Members is the responder's known member roster (peer ids, excluding
	// the requester) for gossip.
	Members []string `json:"members,omitempty"`
}

// queryRespPayload is carried by MsgQueryResp responses.
type queryRespPayload struct {
	// Swarms lists the swarm ids the responder has joined.
	Swarms []string `json:"swarms,omitempty"`
	// Members maps each joined swarm id to its known member peer ids.
	Members map[string][]string `json:"members,omitempty"`
}

// encodePayload marshals v into an envelope payload.
func encodePayload(v any) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode swarm payload: %w", err)
	}
	return data, nil
}

// decodePayload unmarshals an envelope payload into v.
func decodePayload(env Envelope, v any) error {
	if len(env.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Payload, v); err != nil {
		return fmt.Errorf("decode swarm payload: %w", err)
	}
	return nil
}

func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
