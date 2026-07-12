package protocol

import (
	"bytes"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	in := Message{ID: 9, Type: TypeTCPData, ConnID: 7, Seq: 3, Name: "svc", Error: "", Payload: []byte("hello")}
	encoded, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Type != in.Type || out.ConnID != in.ConnID || out.Seq != in.Seq || out.Name != in.Name || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("round trip mismatch: %#v", out)
	}
}

func TestRejectTrailingData(t *testing.T) {
	encoded, _ := Encode(Message{Type: TypePing})
	encoded = append(encoded, 1)
	if _, err := Decode(encoded); err == nil {
		t.Fatal("expected length error")
	}
}
