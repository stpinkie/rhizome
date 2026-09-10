package stream

import (
	"encoding/binary"
	"fmt"
	"io"
)

const FrameHeaderLen = 5

// maxFramePayload is the largest payload the framing layer will encode/decode.
const maxFramePayload = 128 << 20 // 128 MiB

func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxFramePayload {
		return fmt.Errorf("frame payload too large: %d", len(payload))
	}
	header := make([]byte, FrameHeaderLen)
	header[0] = typ
	//nolint:gosec // G115: len(payload) is bounded by maxFramePayload.
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func ReadFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, FrameHeaderLen)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	typ := header[0]
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxFramePayload {
		return 0, nil, fmt.Errorf("frame too large: %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}
