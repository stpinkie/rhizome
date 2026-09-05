package stream

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// peerWrite encodes a frame on w from a goroutine because net.Pipe writes block
// until the peer consumes the bytes.
func peerWrite(w io.Writer, f *ReliableFrame) {
	go func() { _ = f.encode(w) }()
}

// readWireFrame decodes the next reliable frame from r or fails the test.
func readWireFrame(t *testing.T, r io.Reader) *ReliableFrame {
	t.Helper()
	f, err := decodeReliableFrame(r)
	require.NoError(t, err)
	return f
}

// wirePipe returns a ReliableConn on one end of a net.Pipe plus the raw peer
// end. The peer end gets a read deadline so malformed or missing frames fail
// the test instead of hanging it forever.
func wirePipe(t *testing.T, opts ...ReliableOption) (*ReliableConn, net.Conn) {
	t.Helper()
	c1, c2 := net.Pipe()
	require.NoError(t, c2.SetDeadline(time.Now().Add(10*time.Second)))
	rc := NewReliableConn(c1, opts...)
	t.Cleanup(func() {
		_ = c2.Close()
		_ = rc.Close()
	})
	return rc, c2
}

// TestReliableConnRetransmitsOnAckTimeout proves the sender retransmits a data
// frame when no ACK arrives within the write timeout, and that WriteFrame only
// returns after a real ACK.
func TestReliableConnRetransmitsOnAckTimeout(t *testing.T) {
	rc, peer := wirePipe(t, WithWriteTimeout(200*time.Millisecond))

	writeDone := make(chan error, 1)
	go func() { writeDone <- rc.WriteFrame(1, []byte("ping")) }()

	// First delivery.
	f1 := readWireFrame(t, peer)
	require.Equal(t, flagData, f1.Flags)
	require.Equal(t, uint32(0), f1.Seq)
	require.Equal(t, []byte{1, 'p', 'i', 'n', 'g'}, f1.Payload)

	// Without an ACK the frame must not be considered delivered yet.
	select {
	case err := <-writeDone:
		t.Fatalf("WriteFrame returned %v before any ACK", err)
	case <-time.After(100 * time.Millisecond):
	}

	// After the write timeout the frame is retransmitted.
	f2 := readWireFrame(t, peer)
	require.Equal(t, flagData, f2.Flags)
	require.Equal(t, uint32(0), f2.Seq)
	require.Equal(t, f1.Payload, f2.Payload)

	// ACK it; the write should now complete.
	peerWrite(peer, &ReliableFrame{Flags: flagAck, Ack: 0})
	select {
	case err := <-writeDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("WriteFrame did not complete after ACK")
	}
}

// TestReliableConnRetransmitsOnNack proves a NACK for the in-flight sequence
// number causes an immediate retransmission.
func TestReliableConnRetransmitsOnNack(t *testing.T) {
	rc, peer := wirePipe(t, WithWriteTimeout(10*time.Second))

	writeDone := make(chan error, 1)
	go func() { writeDone <- rc.WriteFrame(7, []byte("data")) }()

	f1 := readWireFrame(t, peer)
	require.Equal(t, flagData, f1.Flags)
	require.Equal(t, uint32(0), f1.Seq)

	// NACK the frame: the sender must resend it without waiting for the
	// (very long) write timeout.
	peerWrite(peer, &ReliableFrame{Flags: flagNack, Ack: 0})
	f2 := readWireFrame(t, peer)
	require.Equal(t, flagData, f2.Flags)
	require.Equal(t, uint32(0), f2.Seq)
	require.Equal(t, f1.Payload, f2.Payload)

	peerWrite(peer, &ReliableFrame{Flags: flagAck, Ack: 0})
	select {
	case err := <-writeDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("WriteFrame did not complete after ACK")
	}
}

// TestReliableConnOutOfOrderAndDuplicate verifies the receive side NACKs an
// out-of-order frame, ACKs in-order frames, and re-ACKs (but does not
// re-deliver) duplicates.
func TestReliableConnOutOfOrderAndDuplicate(t *testing.T) {
	rc, peer := wirePipe(t, WithReadTimeout(10*time.Second))

	// Out-of-order data (expected seq 0, got seq 2) triggers a NACK for the
	// expected sequence number.
	peerWrite(peer, &ReliableFrame{Flags: flagData, Seq: 2, Payload: []byte{9, 'x'}})
	f := readWireFrame(t, peer)
	require.Equal(t, flagNack, f.Flags)
	require.Equal(t, uint32(0), f.Ack)

	// The expected frame is ACKed and delivered.
	peerWrite(peer, &ReliableFrame{Flags: flagData, Seq: 0, Payload: []byte{9, 'm', '0'}})
	f = readWireFrame(t, peer)
	require.Equal(t, flagAck, f.Flags)
	require.Equal(t, uint32(0), f.Ack)

	typ, payload, err := rc.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, byte(9), typ)
	require.Equal(t, "m0", string(payload))

	// A duplicate of seq 0 is re-ACKed but not delivered again.
	peerWrite(peer, &ReliableFrame{Flags: flagData, Seq: 0, Payload: []byte{9, 'd', 'u', 'p'}})
	f = readWireFrame(t, peer)
	require.Equal(t, flagAck, f.Flags)
	require.Equal(t, uint32(0), f.Ack)

	// The next in-order frame is ACKed and delivered; ReadFrame must return
	// "m1", proving the duplicate was dropped.
	peerWrite(peer, &ReliableFrame{Flags: flagData, Seq: 1, Payload: []byte{9, 'm', '1'}})
	f = readWireFrame(t, peer)
	require.Equal(t, flagAck, f.Flags)
	require.Equal(t, uint32(1), f.Ack)

	typ, payload, err = rc.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, byte(9), typ)
	require.Equal(t, "m1", string(payload))
}

// TestReliableConnCorruptFrameFailsRead sends a data frame whose payload was
// corrupted after its CRC was computed. The framing layer does not NACK on a
// CRC failure; the read side must surface the corruption as an error and the
// connection shuts down.
func TestReliableConnCorruptFrameFailsRead(t *testing.T) {
	rc, peer := wirePipe(t, WithReadTimeout(10*time.Second))

	payload := []byte{1, 'h', 'i'}
	header := make([]byte, reliableFrameHeaderLen)
	copy(header[0:2], reliableMagic[:])
	header[2] = 1 // version
	header[3] = flagData
	binary.BigEndian.PutUint32(header[12:], uint32(len(payload)))
	crc := crc32.ChecksumIEEE(header[:12])
	crc = crc32Update(crc, payload)
	binary.BigEndian.PutUint32(header[16:], crc)
	payload[1] ^= 0xFF // corrupt a payload byte after computing the CRC

	go func() {
		_, _ = peer.Write(header)
		_, _ = peer.Write(payload)
	}()

	_, _, err := rc.ReadFrame()
	require.Error(t, err)
	require.Contains(t, err.Error(), "crc mismatch")
}

// TestReliableConnReadTimeout proves a read deadline on the underlying stream
// surfaces as a timeout error from ReadFrame.
func TestReliableConnReadTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	rc := NewReliableConn(c1, WithReadTimeout(200*time.Millisecond))
	defer rc.Close()

	start := time.Now()
	_, _, err := rc.ReadFrame()
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrDeadlineExceeded), "expected deadline error, got %v", err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// TestReliableConnWriteFrameFailsWithoutAck proves WriteFrame does not report
// success when the peer never acknowledges the frame.
func TestReliableConnWriteFrameFailsWithoutAck(t *testing.T) {
	rc, peer := wirePipe(t, WithWriteTimeout(50*time.Millisecond), WithMaxRetries(2))

	// Drain incoming frames without ever ACKing them.
	go func() {
		for {
			if _, err := decodeReliableFrame(peer); err != nil {
				return
			}
		}
	}()

	start := time.Now()
	err := rc.WriteFrame(1, []byte("x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not acked")
	assert.Less(t, time.Since(start), 10*time.Second)
}

// TestDecodeReliableFrameRejectsOversized exercises the 128MB sanity limit in
// the reliable frame decoder without allocating the payload.
func TestDecodeReliableFrameRejectsOversized(t *testing.T) {
	pr, pw := io.Pipe()

	header := make([]byte, reliableFrameHeaderLen)
	copy(header[0:2], reliableMagic[:])
	header[2] = 1 // version
	header[3] = flagData
	binary.BigEndian.PutUint32(header[12:], uint32(129<<20)) // > 128MB

	go func() { _, _ = pw.Write(header) }()

	_, err := decodeReliableFrame(pr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
}

// TestDecodeReliableFrameRejectsBadMagic covers the magic/version checks.
func TestDecodeReliableFrameRejectsBadMagic(t *testing.T) {
	pr, pw := io.Pipe()

	header := make([]byte, reliableFrameHeaderLen)
	header[0] = 'X'
	header[1] = 'Y'

	go func() { _, _ = pw.Write(header) }()

	_, err := decodeReliableFrame(pr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid reliable frame magic")
}

// TestReadFrameRejectsOversized exercises the 128MB sanity limit in the plain
// (non-reliable) frame reader used elsewhere.
func TestReadFrameRejectsOversized(t *testing.T) {
	pr, pw := io.Pipe()

	header := make([]byte, FrameHeaderLen)
	header[0] = 1
	binary.BigEndian.PutUint32(header[1:], uint32(129<<20)) // > 128MB

	go func() { _, _ = pw.Write(header) }()

	_, _, err := ReadFrame(pr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "frame too large")
}
