// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hpack

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata golden files from current output")

// buildGoldenEncoder returns a fresh encoder for the named config: the
// default policy, a pinned browser-style profile exercising every
// fingerprinting override, or a tiny table that churns through eviction and
// compaction constantly.
func buildGoldenEncoder(name string, w *bytes.Buffer) *Encoder {
	e := NewEncoder(w)
	switch name {
	case "pinned":
		e.IndexPolicy = IndexingChrome
		e.NeverIndexHeaders = map[string]bool{"authorization": true}
		e.AlwaysIndexHeaders = map[string]bool{"x-keep": true}
		e.Representations = map[string]Representation{
			":path":    RepresentationWithout,
			"x-secret": RepresentationNever,
			"x-pinned": RepresentationIncremental,
		}
		e.SetMaxDynamicTableSizeLimit(65536)
		e.SetMaxDynamicTableSize(65536)
	case "tiny":
		e.SetMaxDynamicTableSize(128)
	}
	return e
}

// goldenStream drives enc through a deterministic pseudo-random field stream
// that hits every search outcome: static exact matches (valued and
// empty-valued entries), static name-only matches with short, long and
// per-call-unique values, dynamic exact matches via repeats, sensitive
// fields, unknown names, and mid-stream table size updates.
func goldenStream(t *testing.T, enc *Encoder, resize func(uint32)) {
	t.Helper()
	rng := rand.New(rand.NewSource(1))

	staticNames := make([]string, 0, staticTable.len())
	staticFields := make([]HeaderField, 0, staticTable.len())
	for k := range staticTable.len() {
		f := staticTable.entry(k)
		staticNames = append(staticNames, f.Name)
		staticFields = append(staticFields, f)
	}

	randValue := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
		}
		return string(b)
	}

	var recent []HeaderField
	write := func(f HeaderField) {
		if err := enc.WriteField(f); err != nil {
			t.Fatalf("WriteField(%+v): %v", f, err)
		}
		if len(recent) < 64 {
			recent = append(recent, f)
		} else {
			recent[rng.Intn(len(recent))] = f
		}
	}

	for i := range 2500 {
		switch rng.Intn(12) {
		case 0, 1: // static exact match, including the empty-valued entries
			write(staticFields[rng.Intn(len(staticFields))])
		case 2: // static name, long value (cookie-sized)
			write(HeaderField{Name: staticNames[rng.Intn(len(staticNames))], Value: randValue(100 + rng.Intn(500))})
		case 3: // static name, short value
			write(HeaderField{Name: staticNames[rng.Intn(len(staticNames))], Value: randValue(1 + rng.Intn(12))})
		case 4: // unknown name
			write(HeaderField{Name: fmt.Sprintf("x-custom-%d", rng.Intn(20)), Value: randValue(1 + rng.Intn(40))})
		case 5, 6: // repeat an earlier field: dynamic exact match territory
			if len(recent) > 0 {
				write(recent[rng.Intn(len(recent))])
				continue
			}
			write(staticFields[rng.Intn(len(staticFields))])
		case 7: // sensitive
			write(HeaderField{Name: staticNames[rng.Intn(len(staticNames))], Value: randValue(1 + rng.Intn(60)), Sensitive: true})
		case 8: // explicit empty value on a static name
			write(HeaderField{Name: staticNames[rng.Intn(len(staticNames))], Value: ""})
		case 9: // same name, unique value every time (transaction-id shaped)
			write(HeaderField{Name: "x-changing", Value: fmt.Sprintf("%08d-%s", i, randValue(24))})
		case 10: // pinned-representation names
			for _, n := range []string{":path", "x-secret", "x-pinned", "x-keep", "authorization"} {
				write(HeaderField{Name: n, Value: randValue(1 + rng.Intn(30))})
			}
		case 11: // occasional table resize, emitting a size update
			if resize != nil && i%500 == 250 {
				resize(uint32(256 + rng.Intn(2048)))
			}
			write(staticFields[rng.Intn(len(staticFields))])
		}
	}
}

// TestEncoderStreamGolden pins the encoder's exact output bytes over the
// deterministic stream. Any change to search, indexing or emission that
// alters a single bit on the wire fails this test; regenerate deliberately
// with -update-golden only for a change that is MEANT to change the wire.
func TestEncoderStreamGolden(t *testing.T) {
	var out bytes.Buffer
	for _, cfg := range []string{"default", "pinned", "tiny"} {
		var buf bytes.Buffer
		enc := buildGoldenEncoder(cfg, &buf)
		resize := func(v uint32) {
			enc.SetMaxDynamicTableSize(v)
		}
		if cfg == "pinned" {
			resize = nil // keep the pinned profile's table stable
		}
		goldenStream(t, enc, resize)
		fmt.Fprintf(&out, "%s %d\n", cfg, buf.Len())
		out.Write(buf.Bytes())
	}

	const goldenPath = "testdata/encoder_stream.golden"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", goldenPath, out.Len())
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden (regenerate with -update-golden): %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		i := 0
		for i < len(want) && i < out.Len() && want[i] == out.Bytes()[i] {
			i++
		}
		t.Fatalf("encoder output diverges from golden: got %d bytes, want %d, first difference at offset %d", out.Len(), len(want), i)
	}
}
