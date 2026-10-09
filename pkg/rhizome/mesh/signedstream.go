package mesh

import (
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
)

// serveSignedStream services one inbound request/response protocol
// stream: read a single request frame, dispatch it, sign the response
// over its canonical marshaled form (signature field cleared), and
// write it back. Shared by the skill and economy protocols — every
// signed-envelope protocol in the mesh follows this shape.
func serveSignedStream[Req, Resp any](
	m *Mesh, s network.Stream,
	wantFrame, respFrame byte,
	handle func(peer.ID, Req) Resp,
	sigOf func(*Resp) *[]byte,
) {
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(15*time.Second))
	defer func() { _ = rc.Close() }()

	typ, raw, err := rc.ReadFrame()
	if err != nil || typ != wantFrame {
		return
	}
	var req Req
	if json.Unmarshal(raw, &req) != nil {
		return
	}
	resp := handle(s.Conn().RemotePeer(), req)

	sig := sigOf(&resp)
	*sig = nil
	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	*sig = identity.Sign(m.id.PrivateKey, payload)
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = rc.WriteFrame(respFrame, data)
}
