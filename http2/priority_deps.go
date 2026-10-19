package http2

import "sync"

// priorityDependencies chooses the parent a new stream names in its HEADERS
// frame, the way Chromium does. It is a port of net/spdy/
// http2_priority_dependencies.cc:
//
//	*parent_stream_id = 0;
//	*exclusive = true;
//	*weight = spdy::Spdy3PriorityToHttp2Weight(priority);
//	// Dependent on the lowest-priority stream that has a priority >= |priority|.
//	IdList::iterator parent;
//	if (PriorityLowerBound(priority, &parent)) {
//	  *parent_stream_id = parent->first;
//	}
//
// where PriorityLowerBound walks from the stream's own band towards the
// highest one and takes the newest stream in the first non-empty band. Streams
// leave the lists when SpdySession closes them, so requests that run at the
// same time form a chain, and a request made on an otherwise quiet connection
// depends on stream 0.
//
// Chromium's bands are SPDY/3 priorities, 0 the highest and 7 the lowest. The
// HEADERS frame only carries the weight, so the band is recovered from it with
// Chromium's own inverse, which maps each of its eight weights back exactly.
type priorityDependencies struct {
	mu    sync.Mutex
	bands [8][]uint32 // stream IDs per band, oldest first
}

// onStreamCreation records streamID in the band its weight belongs to and
// returns the stream it depends on, or 0. weight is the wire value, 0-255.
func (d *priorityDependencies) onStreamCreation(streamID uint32, weight uint8) (parent uint32) {
	band := http2WeightToSpdy3Priority(int(weight) + 1)
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := int(band); i >= 0; i-- {
		if n := len(d.bands[i]); n > 0 {
			parent = d.bands[i][n-1]
			break
		}
	}
	d.bands[band] = append(d.bands[band], streamID)
	return parent
}

// onStreamDestruction forgets streamID. Unknown IDs are ignored: a stream that
// failed before its HEADERS went out was never recorded.
func (d *priorityDependencies) onStreamDestruction(streamID uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for b, ids := range d.bands {
		for i, id := range ids {
			if id == streamID {
				d.bands[b] = append(ids[:i], ids[i+1:]...)
				return
			}
		}
	}
}

// http2WeightToSpdy3Priority is quiche's Http2WeightToSpdy3Priority, float32
// arithmetic included, so a weight lands in the band Chromium would put it in.
//
//	const float kSteps = 255.9f / 7.f;
//	return static_cast<SpdyPriority>(7.f - (weight - 1) / kSteps);
func http2WeightToSpdy3Priority(weight int) uint8 {
	weight = min(max(weight, 1), 256)
	const kSteps = float32(255.9) / float32(7)
	return uint8(float32(7) - float32(weight-1)/kSteps)
}
