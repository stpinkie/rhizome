package stream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const reliableFrameHeaderLen = 20

// maxReliablePayload is the largest payload the reliable framing layer will
// encode/decode.
const maxReliablePayload = 128 << 20 // 128 MiB

const (
	flagData  = byte(1)
	flagAck   = byte(2)
	flagNack  = byte(4)
	flagReset = byte(8)
	flagClose = byte(16)
)

var reliableMagic = [2]byte{'r', 'z'}

// ReliableFrame is the wire format for the reliable framing layer.
type ReliableFrame struct {
	Flags   byte
	Seq     uint32
	Ack     uint32
	Payload []byte
}

func (f *ReliableFrame) encode(w io.Writer) error {
	if len(f.Payload) > maxReliablePayload {
		return fmt.Errorf("reliable frame payload too large: %d", len(f.Payload))
	}
	header := make([]byte, reliableFrameHeaderLen)
	copy(header[0:2], reliableMagic[:])
	header[2] = 1 // version
	header[3] = f.Flags
	binary.BigEndian.PutUint32(header[4:], f.Seq)
	binary.BigEndian.PutUint32(header[8:], f.Ack)
	//nolint:gosec // G115: len(f.Payload) is bounded by maxReliablePayload.
	binary.BigEndian.PutUint32(header[12:], uint32(len(f.Payload)))
	checksum := crc32.ChecksumIEEE(header[:12])
	checksum = crc32Update(checksum, f.Payload)
	binary.BigEndian.PutUint32(header[16:], checksum)

	if err := writeAll(w, header); err != nil {
		return err
	}
	if len(f.Payload) > 0 {
		if err := writeAll(w, f.Payload); err != nil {
			return err
		}
	}
	return nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

func crc32Update(crc uint32, p []byte) uint32 {
	return crc32.Update(crc, crc32.IEEETable, p)
}

func decodeReliableFrame(r io.Reader) (*ReliableFrame, error) {
	header := make([]byte, reliableFrameHeaderLen)
	if _, err := io.ReadFull(r, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		return nil, fmt.Errorf("read reliable frame header: %w", err)
	}
	if header[0] != reliableMagic[0] || header[1] != reliableMagic[1] {
		return nil, fmt.Errorf("invalid reliable frame magic: %x", header[:2])
	}
	if header[2] != 1 {
		return nil, fmt.Errorf("unsupported reliable frame version: %d", header[2])
	}
	flags := header[3]
	seq := binary.BigEndian.Uint32(header[4:])
	ack := binary.BigEndian.Uint32(header[8:])
	length := binary.BigEndian.Uint32(header[12:])
	if length > maxReliablePayload {
		return nil, fmt.Errorf("reliable frame payload too large: %d", length)
	}
	wantCRC := binary.BigEndian.Uint32(header[16:])

	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("read reliable frame payload: %w", err)
		}
	}

	checksum := crc32.ChecksumIEEE(header[:12])
	checksum = crc32Update(checksum, payload)
	if checksum != wantCRC {
		return nil, fmt.Errorf("reliable frame crc mismatch: got %08x want %08x", checksum, wantCRC)
	}

	return &ReliableFrame{Flags: flags, Seq: seq, Ack: ack, Payload: payload}, nil
}

// sendReq carries a user frame to the writer goroutine.
type sendReq struct {
	frame *ReliableFrame
	ack   chan struct{}
	err   chan error
}

// ReliableConn provides stop-and-wait reliable framing on top of a raw
// io.ReadWriteCloser. Each data frame is acknowledged by the peer before the
// next frame is sent. Retransmits and close/reset handling are automatic.
type ReliableConn struct {
	conn         io.ReadWriteCloser
	writeTimeout time.Duration
	// readTimeout is stored atomically because SetReadTimeout may be called
	// concurrently with the reader goroutine.
	readTimeoutVal atomic.Int64
	maxRetries     int
	baseRTT        time.Duration
	closeTimeout   time.Duration

	// read-side state
	rdMu        sync.Mutex
	recvCh      chan recvResult
	nextRecvSeq uint32
	lastAckSent uint32

	// write-side state
	wrMu         sync.Mutex
	sendCh       chan sendReq
	nextSendSeq  uint32
	lastAckRecv  uint32
	ackReceived  bool // lastAckRecv is only meaningful once a real ACK arrived
	unackedFrame *ReliableFrame
	ackCh        chan struct{} // signalled (non-blocking) when an ACK frame arrives

	done           chan struct{}
	closed         bool
	closeMu        sync.Mutex
	closing        bool
	closeAcked     chan struct{}
	closeAckedOnce sync.Once
}

type recvResult struct {
	frame *ReliableFrame
	err   error
}

// ReliableOption tunes the ReliableConn behaviour.
type ReliableOption func(*ReliableConn)

// WithWriteTimeout sets how long the sender waits for an ACK before retransmitting.
func WithWriteTimeout(d time.Duration) ReliableOption {
	return func(r *ReliableConn) { r.writeTimeout = d }
}

// WithReadTimeout sets the read deadline applied while waiting for frames.
func WithReadTimeout(d time.Duration) ReliableOption {
	return func(r *ReliableConn) { r.setReadTimeout(d) }
}

// SetReadTimeout changes the read deadline applied while waiting for frames.
// Safe to call after the connection is created; the next ReadFrame uses the
// new value.
func (r *ReliableConn) SetReadTimeout(d time.Duration) {
	r.setReadTimeout(d)
}

func (r *ReliableConn) setReadTimeout(d time.Duration) {
	r.readTimeoutVal.Store(int64(d))
}

func (r *ReliableConn) getReadTimeout() time.Duration {
	return time.Duration(r.readTimeoutVal.Load())
}

// WithMaxRetries sets the number of retransmission attempts before giving up.
func WithMaxRetries(n int) ReliableOption {
	return func(r *ReliableConn) { r.maxRetries = n }
}

// WithCloseTimeout sets how long Close waits for a close-ack from the peer
// before shutting down the underlying stream. A short timeout reduces the
// window where a stream reset can race an in-flight ACK.
func WithCloseTimeout(d time.Duration) ReliableOption {
	return func(r *ReliableConn) { r.closeTimeout = d }
}

// NewReliableConn wraps a stream with reliable framing. The caller must not use
// the underlying stream after wrapping it.
func NewReliableConn(conn io.ReadWriteCloser, opts ...ReliableOption) *ReliableConn {
	r := &ReliableConn{
		conn:         conn,
		writeTimeout: 1 * time.Second,
		maxRetries:   5,
		baseRTT:      100 * time.Millisecond,
		closeTimeout: 2 * time.Second,
		recvCh:       make(chan recvResult, 1),
		sendCh:       make(chan sendReq, 1),
		done:         make(chan struct{}),
		ackCh:        make(chan struct{}, 1),
		closeAcked:   make(chan struct{}),
	}
	r.setReadTimeout(5 * time.Second)
	for _, o := range opts {
		if o != nil {
			o(r)
		}
	}
	go r.writer()
	go r.reader()
	return r
}

// WriteFrame sends a data frame with the given type and payload, then waits
// for an ACK. The type is sent as the first byte of the reliable payload.
func (r *ReliableConn) WriteFrame(typ byte, payload []byte) error {
	select {
	case <-r.done:
		return errors.New("reliable conn closed")
	default:
	}

	full := make([]byte, 1+len(payload))
	full[0] = typ
	copy(full[1:], payload)

	req := sendReq{
		frame: &ReliableFrame{Flags: flagData, Payload: full},
		ack:   make(chan struct{}, 1),
		err:   make(chan error, 1),
	}

	select {
	case r.sendCh <- req:
	case <-r.done:
		return errors.New("reliable conn closed")
	}

	select {
	case <-req.ack:
		return nil
	case err := <-req.err:
		return err
	case <-r.done:
		// The writer reports the concrete send failure on req.err (or the
		// ack on req.ack) once waitForAck observes r.done and re-checks the
		// ack state. That path is only a few mutex/ channel ops, but it can
		// race this select. Wait a short grace for the in-flight result
		// instead of masking success/real errors with a generic "closed".
		timer := time.NewTimer(1 * time.Second)
		defer timer.Stop()
		select {
		case <-req.ack:
			return nil
		case err := <-req.err:
			return err
		case <-timer.C:
		}
		return errors.New("reliable conn closed")
	}
}

// ReadFrame returns the next in-order data frame type and payload from the peer.
func (r *ReliableConn) ReadFrame() (byte, []byte, error) {
	select {
	case <-r.done:
		return 0, nil, errors.New("reliable conn closed")
	case res := <-r.recvCh:
		if res.err != nil {
			return 0, nil, res.err
		}
		if len(res.frame.Payload) == 0 {
			return 0, nil, errors.New("empty reliable data frame")
		}
		return res.frame.Payload[0], res.frame.Payload[1:], nil
	}
}

// Close sends a close frame and waits for the peer to acknowledge it before
// shutting down the underlying stream. The graceful close prevents the stream
// reset from racing an in-flight ACK.
func (r *ReliableConn) Close() error {
	r.closeMu.Lock()
	if r.closed || r.closing {
		r.closeMu.Unlock()
		return nil
	}
	r.closing = true
	r.closeMu.Unlock()

	_ = r.sendControlFrame(flagClose, 0)

	select {
	case <-r.closeAcked:
	case <-r.done:
	case <-time.After(r.closeTimeout):
	}

	r.shutdown(errors.New("reliable conn closed"))
	return nil
}

func (r *ReliableConn) sendControlFrame(flags byte, ack uint32) error {
	r.wrMu.Lock()
	defer r.wrMu.Unlock()
	if r.writeTimeout > 0 {
		_ = r.setWriteDeadline(time.Now().Add(r.writeTimeout))
	}
	f := &ReliableFrame{Flags: flags, Ack: ack}
	return f.encode(r.conn)
}

func (r *ReliableConn) writer() {
	defer r.shutdown(errors.New("reliable writer stopped"))
	for {
		select {
		case <-r.done:
			return
		case req, ok := <-r.sendCh:
			if !ok {
				return
			}
			if err := r.sendWithRetry(req); err != nil {
				req.err <- err
				return
			}
			req.ack <- struct{}{}
		}
	}
}

func (r *ReliableConn) sendWithRetry(req sendReq) error {
	r.wrMu.Lock()
	seq := r.nextSendSeq
	r.nextSendSeq++
	r.wrMu.Unlock()

	frame := req.frame
	frame.Seq = seq

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		r.wrMu.Lock()
		r.unackedFrame = frame
		r.wrMu.Unlock()

		if err := r.writeFrame(frame); err != nil {
			return err
		}

		timeout := r.writeTimeout
		if attempt > 0 {
			timeout = r.backoff(attempt)
		}

		if r.waitForAck(seq, timeout) {
			r.wrMu.Lock()
			r.unackedFrame = nil
			r.wrMu.Unlock()
			return nil
		}
	}
	return fmt.Errorf("frame %d not acked after %d retries", seq, r.maxRetries)
}

func (r *ReliableConn) writeFrame(f *ReliableFrame) error {
	r.wrMu.Lock()
	defer r.wrMu.Unlock()
	if r.writeTimeout > 0 {
		if err := r.setWriteDeadline(time.Now().Add(r.writeTimeout)); err != nil {
			return err
		}
	}
	return f.encode(r.conn)
}

func (r *ReliableConn) backoff(attempt int) time.Duration {
	d := r.baseRTT * time.Duration(1<<attempt)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func (r *ReliableConn) waitForAck(seq uint32, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		r.wrMu.Lock()
		acked := r.ackReceived && r.lastAckRecv >= seq
		r.wrMu.Unlock()
		if acked {
			return true
		}
		select {
		case <-r.done:
			// An ACK may have landed just before the connection shut down.
			r.wrMu.Lock()
			acked = r.ackReceived && r.lastAckRecv >= seq
			r.wrMu.Unlock()
			return acked
		case <-deadline.C:
			// Same for the write timeout: don't race a just-arrived ACK.
			r.wrMu.Lock()
			acked = r.ackReceived && r.lastAckRecv >= seq
			r.wrMu.Unlock()
			return acked
		case <-r.ackCh:
		}
	}
}

func (r *ReliableConn) reader() {
	defer r.shutdown(errors.New("reliable reader stopped"))
	for {
		select {
		case <-r.done:
			return
		default:
		}

		if d := r.getReadTimeout(); d > 0 {
			if err := r.setReadDeadline(time.Now().Add(d)); err != nil {
				r.recvCh <- recvResult{err: err}
				return
			}
		}

		frame, err := decodeReliableFrame(r.conn)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return
			}
			select {
			case r.recvCh <- recvResult{err: err}:
			case <-r.done:
			}
			return
		}

		switch frame.Flags {
		case flagData:
			r.handleDataFrame(frame)
		case flagAck:
			r.handleAckFrame(frame)
		case flagNack:
			r.handleNackFrame(frame)
		case flagClose:
			// Acknowledge the peer's close so it can stop waiting and shut down
			// without resetting the stream while our ACK is still in flight.
			_ = r.sendControlFrame(flagClose, 0)
			r.closeAckedOnce.Do(func() { close(r.closeAcked) })
			return
		case flagReset:
			r.recvCh <- recvResult{err: errors.New("peer reset reliable conn")}
			return
		default:
			r.recvCh <- recvResult{err: fmt.Errorf("unknown reliable frame flags: %d", frame.Flags)}
			return
		}
	}
}

func (r *ReliableConn) setReadDeadline(t time.Time) error {
	type deadliner interface {
		SetReadDeadline(t time.Time) error
	}
	if d, ok := r.conn.(deadliner); ok {
		return d.SetReadDeadline(t)
	}
	return nil
}

func (r *ReliableConn) setWriteDeadline(t time.Time) error {
	type deadliner interface {
		SetWriteDeadline(t time.Time) error
	}
	if d, ok := r.conn.(deadliner); ok {
		return d.SetWriteDeadline(t)
	}
	return nil
}

func (r *ReliableConn) handleDataFrame(frame *ReliableFrame) {
	r.rdMu.Lock()
	defer r.rdMu.Unlock()

	if frame.Seq < r.nextRecvSeq {
		// Duplicate; ack and ignore.
		_ = r.sendControlFrame(flagAck, frame.Seq)
		return
	}

	if frame.Seq > r.nextRecvSeq {
		// Out of order; nack the expected sequence.
		_ = r.sendControlFrame(flagNack, r.nextRecvSeq)
		return
	}

	// In-order: ack and deliver.
	r.nextRecvSeq++
	r.lastAckSent = frame.Seq
	_ = r.sendControlFrame(flagAck, frame.Seq)

	select {
	case r.recvCh <- recvResult{frame: frame}:
	case <-r.done:
	}
}

func (r *ReliableConn) handleAckFrame(frame *ReliableFrame) {
	r.wrMu.Lock()
	if !r.ackReceived || frame.Ack >= r.lastAckRecv {
		r.lastAckRecv = frame.Ack
		r.ackReceived = true
	}
	r.wrMu.Unlock()
	// Wake any waitForAck so it can re-check the ack state without polling.
	select {
	case r.ackCh <- struct{}{}:
	default:
	}
}

func (r *ReliableConn) handleNackFrame(frame *ReliableFrame) {
	// The retransmit must hold wrMu through the encode: writeFrame and
	// sendControlFrame serialize their writes under the same lock, and a
	// lock-free encode here would interleave bytes with an in-flight write.
	r.wrMu.Lock()
	defer r.wrMu.Unlock()
	f := r.unackedFrame
	if f != nil && frame.Ack == f.Seq {
		if r.writeTimeout > 0 {
			_ = r.setWriteDeadline(time.Now().Add(r.writeTimeout))
		}
		_ = f.encode(r.conn)
	}
}

func (r *ReliableConn) shutdown(err error) {
	r.closeMu.Lock()
	if !r.closed {
		r.closed = true
		close(r.done)
		_ = r.conn.Close()
	}
	r.closeMu.Unlock()

	select {
	case r.recvCh <- recvResult{err: err}:
	default:
	}
}
