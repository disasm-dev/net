// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package httpcommon

import (
	"slices"
	"strconv"
	"testing"
)

// The scratch outlives the request, and after encoding it still pointed at
// the request's value slices, so an idle connection kept the last request's
// Authorization and cookies reachable. release clears them once EncodeHeaders
// is done, across the scratch's whole capacity.
func TestPlanCacheReleasesRequestValues(t *testing.T) {
	c := NewHeaderPlanCache()
	order := []string{"authorization", "accept", "cookie"}
	for i := 0; i < 3; i++ {
		header := map[string][]string{
			"Authorization": {"Bearer secret" + strconv.Itoa(i)},
			"Accept":        {"*/*"},
			"Cookie":        {"a=1"},
		}
		param := testParam(header, order)
		param.PlanCache = c
		if _, err := EncodeHeaders(t.Context(), param, func(string, string) {}); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range c.scratch.plan[:cap(c.scratch.plan)] {
		if p.values != nil {
			t.Errorf("scratch.plan[%d] still holds %q", i, p.values)
		}
	}
	for i, e := range c.scratch.entries[:cap(c.scratch.entries)] {
		if e.values != nil {
			t.Errorf("scratch.entries[%d] still holds %q", i, e.values)
		}
	}
	for i, v := range c.scratch.vals[:cap(c.scratch.vals)] {
		if v != nil {
			t.Errorf("scratch.vals[%d] still holds %q", i, v)
		}
	}
}

// A shape whose identity matches but whose content does not can only be a
// hash collision. plan must fall back to a full build, drop the colliding
// shape, and admit the arriving identity again on its next miss. Forced by
// giving a cached shape another input's identity.
func TestPlanCacheCollisionRebuilds(t *testing.T) {
	c := NewHeaderPlanCache()
	orderA := []string{"accept", "x-a"}
	hA := map[string][]string{"Accept": {"1"}, "X-A": {"a"}}
	orderB := []string{"x-b", "accept"}
	hB := map[string][]string{"Accept": {"2"}, "X-B": {"b"}}

	a := prime(t, c, orderA, hA)
	idB := c.identity(orderB, hB)
	a.id = idB

	got := render(c.plan(orderB, hB))
	if want := render(planHeaderOrder(orderB, hB)); !slices.Equal(got, want) {
		t.Fatalf("collision served %q, want the rebuilt %q", got, want)
	}
	if slices.Contains(c.shapes, a) {
		t.Fatal("the colliding shape was kept")
	}
	if c.find(idB) != nil {
		t.Fatal("the arriving shape was admitted on its first miss")
	}
	c.plan(orderB, hB)
	if c.find(idB) == nil {
		t.Fatal("the arriving shape was not admitted on its second miss")
	}
}

// Past headerPlanShapes shapes, the least recently used one goes.
func TestPlanCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := NewHeaderPlanCache()
	type shape struct {
		order []string
		h     map[string][]string
	}
	mk := func(i int) shape {
		name := "x-shape-" + strconv.Itoa(i)
		return shape{[]string{name, "accept"}, map[string][]string{"Accept": {"*/*"}, name: {"v"}}}
	}
	var shapes []shape
	for i := 0; i < headerPlanShapes; i++ {
		s := mk(i)
		shapes = append(shapes, s)
		prime(t, c, s.order, s.h)
	}
	// Touch shape 0 so shape 1 becomes the least recently used.
	c.plan(shapes[0].order, shapes[0].h)
	extra := mk(headerPlanShapes)
	prime(t, c, extra.order, extra.h)

	if len(c.shapes) != headerPlanShapes {
		t.Fatalf("%d shapes cached, want %d", len(c.shapes), headerPlanShapes)
	}
	if c.find(c.identity(shapes[1].order, shapes[1].h)) != nil {
		t.Error("the least recently used shape survived")
	}
	if c.find(c.identity(shapes[0].order, shapes[0].h)) == nil {
		t.Error("the recently used shape was evicted")
	}
}
