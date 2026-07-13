package streaming

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecodeMultilineAndWrite(t *testing.T) {
	d := NewDecoder(strings.NewReader("event: tick\ndata: one\ndata: two\n\n"), 1024)
	e, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "tick" || string(e.Data) != "one\ntwo" {
		t.Fatalf("unexpected %#v", e)
	}
	var b bytes.Buffer
	if err = Write(&b, e.Name, e.Data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "data: one\ndata: two") {
		t.Fatal(b.String())
	}
}
func TestEventLimit(t *testing.T) {
	d := NewDecoder(strings.NewReader("data: too-long\n\n"), 5)
	if _, err := d.Next(); err == nil {
		t.Fatal("expected size error")
	}
}

func TestDecodePreservesDataWhitespaceAndDispatchesAtEOF(t *testing.T) {
	d := NewDecoder(strings.NewReader(": comment\nevent: tick\ndata:  leading and trailing  "), 1024)
	e, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "tick" || string(e.Data) != " leading and trailing  " {
		t.Fatalf("SSE field semantics changed: %#v", e)
	}
}
