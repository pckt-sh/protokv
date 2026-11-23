// Package codec encodes protobuf messages stored as KVrocks values into a
// versioned envelope, and decodes both the envelope and legacy raw values.
//
// Version 0 (legacy) is raw protobuf bytes, with no header.
//
// Version 1 prefixes the protobuf payload with an 18 bytes header:
//
//	offset size field
//	0      1    magic (0xFF)
//	1      1    version (0x01)
//	2      8    write timestamp, unix nanoseconds, int64 little-endian
//	10     8    xxhash64 of the payload, little-endian
//	18     n    protobuf payload
//
// The magic byte can't be mistaken for a legacy value: the first byte of a
// non-empty protobuf message is a tag whose low 3 bits are its wire type, and
// 0xFF would be wire type 7, which doesn't exist. An empty legacy message is
// a zero-length value.
package codec

import (
	"encoding/binary"
	"time"

	"github.com/cespare/xxhash/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	// Magic marks a value as an envelope, followed by its version.
	Magic byte = 0xFF

	// V0 is a legacy value: raw protobuf bytes without header.
	V0 uint8 = 0
	// V1 is magic + version + timestamp + hash + protobuf payload.
	V1 uint8 = 1

	// HeaderSize is the size of a V1 header.
	HeaderSize = 18
)

var (
	ErrCorrupt            = status.Error(codes.DataLoss, "value header is truncated")
	ErrChecksum           = status.Error(codes.DataLoss, "value checksum mismatch")
	ErrUnsupportedVersion = status.Error(codes.Unimplemented, "unsupported value version")
)

var marshalOpts = proto.MarshalOptions{Deterministic: true}

// Meta describes a stored value. Timestamp and Hash are zero for V0 values.
type Meta struct {
	Version   uint8
	Timestamp time.Time
	Hash      uint64
}

// Encode marshals msg into a V1 value, stamped with the current time.
func Encode(msg proto.Message) ([]byte, error) {
	return EncodeAt(msg, time.Now())
}

// EncodeAt marshals msg into a V1 value stamped with ts.
// A zero ts is stored as 0.
func EncodeAt(msg proto.Message, ts time.Time) ([]byte, error) {
	buf := make([]byte, HeaderSize, HeaderSize+marshalOpts.Size(msg))

	buf, err := marshalOpts.MarshalAppend(buf, msg)
	if err != nil {
		return nil, err
	}

	var nanos int64
	if !ts.IsZero() {
		nanos = ts.UnixNano()
	}

	buf[0] = Magic
	buf[1] = V1
	binary.LittleEndian.PutUint64(buf[2:10], uint64(nanos))
	binary.LittleEndian.PutUint64(buf[10:18], xxhash.Sum64(buf[HeaderSize:]))

	return buf, nil
}

// Split parses the header of a stored value, verifies its checksum and
// returns the protobuf payload, as a sub-slice of b.
func Split(b []byte) ([]byte, Meta, error) {
	if len(b) == 0 || b[0] != Magic {
		return b, Meta{Version: V0}, nil
	}

	if len(b) < 2 {
		return nil, Meta{}, ErrCorrupt
	}

	switch b[1] {
	case V1:
		if len(b) < HeaderSize {
			return nil, Meta{}, ErrCorrupt
		}

		payload := b[HeaderSize:]
		meta := Meta{
			Version: V1,
			Hash:    binary.LittleEndian.Uint64(b[10:18]),
		}

		if xxhash.Sum64(payload) != meta.Hash {
			return nil, Meta{}, ErrChecksum
		}

		if nanos := int64(binary.LittleEndian.Uint64(b[2:10])); nanos != 0 {
			meta.Timestamp = time.Unix(0, nanos)
		}

		return payload, meta, nil

	default:
		return nil, Meta{}, ErrUnsupportedVersion
	}
}

// Decode unmarshals a stored value, of any version, into msg.
func Decode(b []byte, msg proto.Message) (Meta, error) {
	payload, meta, err := Split(b)
	if err != nil {
		return Meta{}, err
	}

	if err := proto.Unmarshal(payload, msg); err != nil {
		return Meta{}, err
	}

	return meta, nil
}
