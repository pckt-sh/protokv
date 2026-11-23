package codec

import (
	"errors"
	"testing"
	"time"

	"github.com/pckt-sh/protokv/internal/testpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func testProduct() *testpb.Product {
	return &testpb.Product{
		Id:       "123",
		Title:    "A product",
		Slug:     "a-product",
		Variants: []*testpb.Variant{{Tag: "red"}, {Tag: "blue"}},
	}
}

func TestRoundTripV1(t *testing.T) {
	ts := time.Unix(0, 1_763_900_000_123_456_789)

	b, err := EncodeAt(testProduct(), ts)
	if err != nil {
		t.Fatal(err)
	}

	if b[0] != Magic || b[1] != V1 {
		t.Fatalf("wrong header: %x", b[:2])
	}

	var got testpb.Product
	meta, err := Decode(b, &got)
	if err != nil {
		t.Fatal(err)
	}

	if !proto.Equal(&got, testProduct()) {
		t.Errorf("message mismatch: %v", &got)
	}
	if meta.Version != V1 {
		t.Errorf("wrong version: %d", meta.Version)
	}
	if !meta.Timestamp.Equal(ts) {
		t.Errorf("wrong timestamp: %v", meta.Timestamp)
	}
	if meta.Hash == 0 {
		t.Error("empty hash")
	}
}

func TestEncodeNow(t *testing.T) {
	before := time.Now()

	b, err := Encode(testProduct())
	if err != nil {
		t.Fatal(err)
	}

	_, meta, err := Split(b)
	if err != nil {
		t.Fatal(err)
	}

	if meta.Timestamp.Before(before) || meta.Timestamp.After(time.Now()) {
		t.Errorf("timestamp out of range: %v", meta.Timestamp)
	}
}

func TestZeroTimestamp(t *testing.T) {
	b, err := EncodeAt(testProduct(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	_, meta, err := Split(b)
	if err != nil {
		t.Fatal(err)
	}

	if !meta.Timestamp.IsZero() {
		t.Errorf("expected zero timestamp, got: %v", meta.Timestamp)
	}
}

func TestEmptyMessageV1(t *testing.T) {
	b, err := Encode(&testpb.Product{})
	if err != nil {
		t.Fatal(err)
	}

	if len(b) != HeaderSize {
		t.Errorf("expected header only, got %d bytes", len(b))
	}

	var got testpb.Product
	if _, err := Decode(b, &got); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeV0(t *testing.T) {
	raw, err := proto.Marshal(testProduct())
	if err != nil {
		t.Fatal(err)
	}

	for name, b := range map[string][]byte{"message": raw, "empty": {}, "nil": nil} {
		t.Run(name, func(t *testing.T) {
			var got testpb.Product
			meta, err := Decode(b, &got)
			if err != nil {
				t.Fatal(err)
			}

			if meta != (Meta{Version: V0}) {
				t.Errorf("wrong meta: %+v", meta)
			}
			if len(b) > 0 && !proto.Equal(&got, testProduct()) {
				t.Errorf("message mismatch: %v", &got)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	valid, err := Encode(testProduct())
	if err != nil {
		t.Fatal(err)
	}

	flipped := append([]byte{}, valid...)
	flipped[len(flipped)-1] ^= 0x01

	badHash := append([]byte{}, valid...)
	badHash[12] ^= 0x01

	tests := map[string]struct {
		b   []byte
		err error
	}{
		"magic only":       {[]byte{Magic}, ErrCorrupt},
		"truncated header": {valid[:HeaderSize-1], ErrCorrupt},
		"flipped payload":  {flipped, ErrChecksum},
		"flipped hash":     {badHash, ErrChecksum},
		"unknown version":  {append([]byte{Magic, 0x02}, valid[2:]...), ErrUnsupportedVersion},
		"version zero":     {append([]byte{Magic, 0x00}, valid[2:]...), ErrUnsupportedVersion},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var got testpb.Product
			if _, err := Decode(tt.b, &got); !errors.Is(err, tt.err) {
				t.Errorf("expected %v, got: %v", tt.err, err)
			}
		})
	}
}

// FuzzMagic checks that no valid protobuf value starts with the magic byte,
// so legacy values are never mistaken for an envelope, and that Decode
// never panics.
func FuzzMagic(f *testing.F) {
	raw, _ := proto.Marshal(testProduct())
	v1, _ := Encode(testProduct())
	f.Add(raw)
	f.Add(v1)
	f.Add([]byte{Magic})
	f.Add([]byte{Magic, V1})

	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 0 && b[0] == Magic {
			// emptypb.Empty keeps unknown fields: it parses any valid wire data.
			if err := proto.Unmarshal(b, &emptypb.Empty{}); err == nil {
				t.Fatalf("valid protobuf starting with magic byte: %x", b)
			}
		}

		_, _ = Decode(b, &emptypb.Empty{})
	})
}

func BenchmarkEncode(b *testing.B) {
	msg := testProduct()

	b.Run("raw", func(b *testing.B) {
		for b.Loop() {
			_, _ = proto.Marshal(msg)
		}
	})

	b.Run("v1", func(b *testing.B) {
		for b.Loop() {
			_, _ = Encode(msg)
		}
	})
}

func BenchmarkDecode(b *testing.B) {
	raw, _ := proto.Marshal(testProduct())
	v1, _ := Encode(testProduct())

	b.Run("raw", func(b *testing.B) {
		for b.Loop() {
			_ = proto.Unmarshal(raw, &testpb.Product{})
		}
	})

	b.Run("v0", func(b *testing.B) {
		for b.Loop() {
			_, _ = Decode(raw, &testpb.Product{})
		}
	})

	b.Run("v1", func(b *testing.B) {
		for b.Loop() {
			_, _ = Decode(v1, &testpb.Product{})
		}
	})
}

func TestParseHeader(t *testing.T) {
	ts := time.Unix(0, 1_763_900_000_000_000_000)
	v1, meta, err := EncodeWithMeta(testProduct(), ts)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := ParseHeader(v1[:HeaderSize])
	if !ok || got.Hash != meta.Hash || !got.Timestamp.Equal(ts) || got.Version != V1 {
		t.Errorf("ParseHeader = %+v, %v; want %+v", got, ok, meta)
	}

	raw, _ := proto.Marshal(testProduct())
	unknown := append([]byte{Magic, 0x02}, v1[2:]...)

	for name, b := range map[string][]byte{
		"legacy":          raw,
		"empty":           nil,
		"short":           v1[:HeaderSize-1],
		"unknown version": unknown,
	} {
		if _, ok := ParseHeader(b); ok {
			t.Errorf("%s: parsed", name)
		}
	}
}
