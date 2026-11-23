package operations

// Line is one captured output line. Seq counts lines per job from 1 and keeps
// counting through evictions, so after eviction the first retained Seq is
// DroppedLines+1.
type Line struct {
	Seq  int
	Text string
}

// Default output bounds per job.
const (
	DefaultMaxLines = 5000
	DefaultMaxBytes = 1 << 20
)

// outputRing keeps the newest lines within a line bound and a byte bound. It
// is not safe for concurrent use; the owning job's mutex guards it.
type outputRing struct {
	maxLines int
	maxBytes int
	lines    []Line
	bytes    int
	lastSeq  int
	dropped  int
}

func newOutputRing(maxLines, maxBytes int) *outputRing {
	return &outputRing{maxLines: maxLines, maxBytes: maxBytes}
}

// add appends a line, evicts the oldest lines until both bounds hold (the new
// line itself goes if it alone exceeds the byte bound), and returns its Seq.
func (r *outputRing) add(text string) int {
	r.lastSeq++
	r.lines = append(r.lines, Line{Seq: r.lastSeq, Text: text})
	r.bytes += len(text)
	for len(r.lines) > 0 && (len(r.lines) > r.maxLines || r.bytes > r.maxBytes) {
		r.bytes -= len(r.lines[0].Text)
		r.lines[0] = Line{} // release the string
		r.lines = r.lines[1:]
		r.dropped++
	}
	return r.lastSeq
}

// after returns a copy of the retained lines with Seq greater than seq.
func (r *outputRing) after(seq int) []Line {
	var out []Line
	for i, l := range r.lines {
		if l.Seq > seq {
			out = append(out, r.lines[i:]...)
			break
		}
	}
	return out
}
