package link

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

// MaxFrameBytes bounds one frame on the link's TLS stream.
const MaxFrameBytes = 128 << 10

// Frame is one request or reply on the link: an operation and its fields. Error is a code
// and Step the next step, for a refused request.
type Frame struct {
	Op     string          `json:"op"`
	Body   json.RawMessage `json:"body,omitempty"`
	Error  string          `json:"error,omitempty"`
	Detail string          `json:"detail,omitempty"`
}

// WriteFrame writes one frame: a 4-byte big-endian length, then the JSON.
func WriteFrame(w io.Writer, f Frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(data) > MaxFrameBytes {
		return errors.New("link_frame_too_large")
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(data)))
	_, err = w.Write(append(head[:], data...))
	return err
}

// ReadFrame reads one frame. A frame larger than MaxFrameBytes, or one with unknown fields,
// is refused.
func ReadFrame(r io.Reader) (Frame, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > MaxFrameBytes {
		return Frame{}, errors.New("link_frame_invalid")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return Frame{}, err
	}
	var f Frame
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&f) != nil || dec.Decode(new(any)) != io.EOF || f.Op == "" {
		return Frame{}, errors.New("link_frame_invalid")
	}
	return f, nil
}

// Decode reads a frame body into v, refusing unknown fields.
func (f Frame) Decode(v any) error {
	dec := json.NewDecoder(bytes.NewReader(f.Body))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(new(any)) != io.EOF {
		return errors.New("link_frame_invalid")
	}
	return nil
}

// NewFrame builds a frame with a JSON body.
func NewFrame(op string, body any) (Frame, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Op: op, Body: data}, nil
}
