// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hpack

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"
)

// churnFields returns n fields whose values never repeat, so every field is
// encoded as a literal with incremental indexing and inserts into the dynamic
// table.
func churnFields(n, start int) []HeaderField {
	fields := make([]HeaderField, n)
	filler := strings.Repeat("a", 48)
	for i := range fields {
		fields[i] = HeaderField{
			Name:  "x-header-" + strconv.Itoa(i%16),
			Value: "value-" + strconv.Itoa(start+i) + "-" + filler,
		}
	}
	return fields
}

// BenchmarkDecoderTableChurn decodes header blocks whose fields all carry
// never-repeating values into a Chrome-sized dynamic table, so steady-state
// decoding inserts and evicts an entry per field.
func BenchmarkDecoderTableChurn(b *testing.B) {
	var buf bytes.Buffer
	e := NewEncoder(&buf)
	for _, f := range churnFields(512, 0) {
		if err := e.WriteField(f); err != nil {
			b.Fatal(err)
		}
	}
	block := buf.Bytes()
	d := NewDecoder(65536, func(HeaderField) {})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := d.Write(block); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncoderTableChurn encodes never-repeating fields into a
// Chrome-sized dynamic table, so steady-state encoding inserts and evicts an
// entry per field.
func BenchmarkEncoderTableChurn(b *testing.B) {
	e := NewEncoder(io.Discard)
	e.SetMaxDynamicTableSizeLimit(65536)
	e.SetMaxDynamicTableSize(65536)
	// Cycling through 8 pregenerated blocks keeps value generation out of the
	// loop; by the time a block comes around again its entries have long been
	// evicted, so no field ever gets an index match.
	blocks := make([][]HeaderField, 8)
	for i := range blocks {
		blocks[i] = churnFields(512, i*512)
	}
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		for _, f := range blocks[i%len(blocks)] {
			if err := e.WriteField(f); err != nil {
				b.Fatal(err)
			}
		}
		i++
	}
}

// requestFields is a request-shaped header list: pseudo-headers that
// exact-match the static table, common names whose values the static table
// does not hold (several hundred bytes of cookie among them) and one
// per-request unique field. The shape every browser and API client sends.
func requestFields(i int) []HeaderField {
	return []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":authority", Value: "www.example.com"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/some/resource/path?page=2&sort=recent"},
		{Name: "user-agent", Value: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"},
		{Name: "accept", Value: "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"},
		{Name: "accept-encoding", Value: "gzip, deflate, br, zstd"},
		{Name: "accept-language", Value: "en-US,en;q=0.9"},
		{Name: "cookie", Value: "session=" + strings.Repeat("s", 200) + "; token=" + strings.Repeat("t", 260) + "; pref=dark"},
		{Name: "authorization", Value: "Bearer " + strings.Repeat("b", 90)},
		{Name: "referer", Value: "https://www.example.com/"},
		{Name: "x-request-id", Value: "req-" + strconv.Itoa(i) + "-0123456789abcdef"},
	}
}

// responseFields is a response-shaped header list, the server-side encoder's
// steady diet: a static-table :status hit and short metadata values.
func responseFields(i int) []HeaderField {
	statuses := [...]string{"200", "200", "200", "304", "404", "500"}
	return []HeaderField{
		{Name: ":status", Value: statuses[i%len(statuses)]},
		{Name: "content-type", Value: "text/html; charset=utf-8"},
		{Name: "content-length", Value: strconv.Itoa(512 + i%4096)},
		{Name: "date", Value: "Tue, 09 Sep 2026 12:00:00 GMT"},
		{Name: "server", Value: "example-server"},
		{Name: "cache-control", Value: "private, max-age=0"},
		{Name: "etag", Value: "\"" + strconv.Itoa(i) + "-abcdef\""},
	}
}

func benchmarkWriteFields(b *testing.B, fields func(int) []HeaderField) {
	e := NewEncoder(io.Discard)
	e.SetMaxDynamicTableSizeLimit(65536)
	e.SetMaxDynamicTableSize(65536)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		for _, f := range fields(i) {
			if err := e.WriteField(f); err != nil {
				b.Fatal(err)
			}
		}
		i++
	}
}

// BenchmarkEncoderWriteFieldRequest encodes request-shaped header lists into
// one long-lived encoder, so repeated fields become dynamic-table hits and
// the unique field churns the table, as on a live connection.
func BenchmarkEncoderWriteFieldRequest(b *testing.B) {
	benchmarkWriteFields(b, requestFields)
}

// BenchmarkEncoderWriteFieldResponse does the same for response-shaped lists.
func BenchmarkEncoderWriteFieldResponse(b *testing.B) {
	benchmarkWriteFields(b, responseFields)
}

// BenchmarkEncoderSearchTableRequest measures the table search alone over the
// request shape, with the dynamic table prewarmed by one full pass.
func BenchmarkEncoderSearchTableRequest(b *testing.B) {
	e := NewEncoder(io.Discard)
	e.SetMaxDynamicTableSizeLimit(65536)
	e.SetMaxDynamicTableSize(65536)
	fields := requestFields(0)
	for _, f := range fields {
		if err := e.WriteField(f); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, f := range fields {
			benchSearchSink, _ = e.searchTable(f)
		}
	}
}

var benchSearchSink uint64
