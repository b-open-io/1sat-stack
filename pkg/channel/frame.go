package channel

import (
	"encoding/binary"
	"fmt"
	"io"
)

const headerSize = 8

// WriteFrame writes a framed message: [4 bytes channel_id][4 bytes length][payload].
func WriteFrame(w io.Writer, channelID uint32, payload []byte) error {
	var header [headerSize]byte
	binary.BigEndian.PutUint32(header[0:4], channelID)
	binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a framed message, returning the channel ID and payload.
func ReadFrame(r io.Reader) (channelID uint32, payload []byte, err error) {
	var header [headerSize]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return 0, nil, fmt.Errorf("read frame header: %w", err)
	}
	channelID = binary.BigEndian.Uint32(header[0:4])
	length := binary.BigEndian.Uint32(header[4:8])
	if length == 0 {
		return channelID, nil, nil
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("read frame payload: %w", err)
	}
	return channelID, payload, nil
}
