package hpack

import (
	"bytes"
	"testing"
)

// A name pinned as incremental and also listed as never-indexed went out as
// never-indexed (0x10), which the peer does not add to its table, while the
// encoder added it to its own. The next reference past that entry pointed at
// different fields on the two sides, and the peer failed to decode. The
// never-index setting outranks the pin, as WriteField documents, so the field
// must stay out of the encoder's table too.
func TestNeverIndexOutranksPinnedIncremental(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	enc.Representations = map[string]Representation{"x-tok": RepresentationIncremental}
	enc.NeverIndexHeaders = map[string]bool{"x-tok": true}
	enc.AlwaysIndexHeaders = map[string]bool{"x-other": true}

	dec := NewDecoder(4096, nil)
	fields := []HeaderField{
		{Name: "x-other", Value: "a"},
		{Name: "x-tok", Value: "secret"},
		{Name: "x-other", Value: "a"},
		{Name: "x-tok", Value: "secret"},
	}
	for i, f := range fields {
		buf.Reset()
		if err := enc.WriteField(f); err != nil {
			t.Fatal(err)
		}
		if f.Name == "x-tok" && buf.Bytes()[0]&0xf0 != 0x10 {
			t.Fatalf("field %d: x-tok went out as %#x, want the never-indexed form", i, buf.Bytes()[0])
		}
		got, err := dec.DecodeFull(buf.Bytes())
		if err != nil {
			t.Fatalf("field %d (%s): peer failed to decode: %v", i, f.Name, err)
		}
		if len(got) != 1 || got[0].Name != f.Name || got[0].Value != f.Value {
			t.Fatalf("field %d: peer decoded %v, want %v", i, got, f)
		}
	}
	if enc.dynTab.table.len() != dec.dynTab.table.len() {
		t.Fatalf("encoder table holds %d entries, decoder %d", enc.dynTab.table.len(), dec.dynTab.table.len())
	}
}
