package http2

import "sync"

// streamDeps reproduces Chromium's Http2PriorityDependencies for
// Transport.ChainStreamDependencies.
//
// Chromium keeps one list of open streams per SPDY priority, 0 the highest.
// A new stream's parent is the last stream in the nearest list at or above
// its own priority, and it is always exclusive, so the tree is a single
// chain ordered by priority and then by age. A stream leaves its list when
// it is destroyed. Measured against Chrome 147 and 155 over five page loads,
// every HEADERS frame on a shared connection named the parent this rule
// gives.
//
// The lock is a leaf: it is taken under cc.wmu (when HEADERS are written)
// and under cc.mu (when a stream ends), and nothing else is ever acquired
// while it is held.
type streamDeps struct {
	mu    sync.Mutex
	lists [8][]uint32 // stream IDs per SPDY priority, oldest first
}

// open records a stream and returns the parent its HEADERS frame names.
// weight is the wire value, the effective weight minus one.
func (d *streamDeps) open(id uint32, weight uint8) uint32 {
	pri := weightToSpdy3Priority(int(weight) + 1)
	d.mu.Lock()
	defer d.mu.Unlock()
	var parent uint32
	for i := int(pri); i >= 0; i-- {
		if l := d.lists[i]; len(l) > 0 {
			parent = l[len(l)-1]
			break
		}
	}
	d.lists[pri] = append(d.lists[pri], id)
	return parent
}

// close removes a stream; closing one that was never opened, or twice, is
// harmless.
func (d *streamDeps) close(id uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, l := range d.lists {
		for j, v := range l {
			if v == id {
				d.lists[i] = append(l[:j:j], l[j+1:]...)
				return
			}
		}
	}
}

// weightToSpdy3Priority is quiche's Http2WeightToSpdy3Priority: the inverse
// of Spdy3PriorityToHttp2Weight, int(255.9/7 * (7-p)) + 1.
func weightToSpdy3Priority(weight int) uint8 {
	if weight < 1 {
		weight = 1
	}
	if weight > 256 {
		weight = 256
	}
	const steps = float32(255.9) / 7
	return uint8(7 - float32(weight-1)/steps)
}
