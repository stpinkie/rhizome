package stream

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memPipe returns two full-duplex in-memory io.ReadWriteClosers. It uses two
// io.Pipe pairs and does not support deadlines, so the ReliableConn defaults do
// not time out during the basic round-trip tests.
func memPipe() (io.ReadWriteCloser, io.ReadWriteCloser) {
	ar, aw := io.Pipe()
	br, bw := io.Pipe()
	return &memConn{reader: ar, writer: bw}, &memConn{reader: br, writer: aw}
}

type memConn struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (m *memConn) Read(p []byte) (int, error)  { return m.reader.Read(p) }
func (m *memConn) Write(p []byte) (int, error) { return m.writer.Write(p) }
func (m *memConn) Close() error                { _ = m.reader.Close(); return m.writer.Close() }

func TestReliableFrame_EncodeDecode(t *testing.T) {
	pr, pw := io.Pipe()
	f := &ReliableFrame{Flags: flagData, Seq: 1, Ack: 0, Payload: []byte{7, 'h', 'e', 'l', 'l', 'o'}}

	done := make(chan struct{})
	var got *ReliableFrame
	var err error
	go func() {
		defer close(done)
		got, err = decodeReliableFrame(pr)
	}()

	require.NoError(t, f.encode(pw))
	require.NoError(t, pw.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for decode")
	}
	require.NoError(t, err)
	assert.Equal(t, f.Flags, got.Flags)
	assert.Equal(t, f.Seq, got.Seq)
	assert.Equal(t, f.Ack, got.Ack)
	assert.Equal(t, f.Payload, got.Payload)
}

func TestReliableConn_BasicRoundTrip(t *testing.T) {
	c1, c2 := memPipe()
	defer c1.Close()
	defer c2.Close()

	r1 := NewReliableConn(c1, WithWriteTimeout(5*time.Second), WithReadTimeout(5*time.Second))
	r2 := NewReliableConn(c2, WithWriteTimeout(5*time.Second), WithReadTimeout(5*time.Second))

	done := make(chan struct{})
	go func() {
		defer close(done)
		typ, payload, err := r2.ReadFrame()
		require.NoError(t, err)
		assert.Equal(t, byte(7), typ)
		assert.Equal(t, "hello", string(payload))

		require.NoError(t, r2.WriteFrame(8, []byte("world")))
	}()

	require.NoError(t, r1.WriteFrame(7, []byte("hello")))
	typ, payload, err := r1.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, byte(8), typ)
	assert.Equal(t, "world", string(payload))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for receiver")
	}

	_ = r1.Close()
	_ = r2.Close()
}
