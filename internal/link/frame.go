package link

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

// MaxFrameBytes bounds one frame on the link's TLS stream: its JSON, after the 4-byte length.
const MaxFrameBytes = 128 << 10

// MaxMaintainerFrameBytes bounds a frame of the operation maintainer and its reply, which carry
// one signed envelope or one answer of at most 1 MiB (#693; decision.md, Answer shapes item 4).
const MaxMaintainerFrameBytes = 1 << 20

// ErrFrameTooLarge refuses a frame larger than its operation's bound.
var ErrFrameTooLarge = errors.New("link_frame_too_large")

// FrameLimit is the bound of a frame of op.
func FrameLimit(op string) int {
	if op == "maintainer" || op == "maintainer-ok" {
		return MaxMaintainerFrameBytes
	}
	return MaxFrameBytes
}

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
	if len(data) > FrameLimit(f.Op) {
		return ErrFrameTooLarge
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(data)))
	_, err = w.Write(append(head[:], data...))
	return err
}

// ReadFrame reads one frame. A frame larger than its operation's bound, or one with unknown
// fields, is refused; the operation is known only after the read, so the read takes up to the
// largest bound.
func ReadFrame(r io.Reader) (Frame, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > MaxMaintainerFrameBytes {
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
	if int(n) > FrameLimit(f.Op) {
		return Frame{}, ErrFrameTooLarge
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
