package dockerc

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func heapNow() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// heapGrowth runs f, which returns something to keep alive, and reports how
// much heap the returned value retains once garbage is collected.
func heapGrowth(f func() any) (uint64, any) {
	base := heapNow()
	keep := f()
	now := heapNow()
	if now < base {
		return 0, keep
	}
	return now - base, keep
}

// The kept lines must not pin the buffers they were cut from. 1001 entries of
// about 60 KB each, tail 1000: 1000 lines of 4000 bytes are about 4 MB, while
// pinned source buffers would hold about 60 MB.
func TestDecodeRetainedMemoryIsBounded(t *testing.T) {
	const limit = 16 << 20
	for _, bad := range []bool{false, true} { // true takes the invalid UTF-8 path
		prefix := ""
		if bad {
			prefix = "\xff"
		}
		line := prefix + strings.Repeat("x", 60000)
		for _, tty := range []bool{false, true} {
			grew, keep := heapGrowth(func() any {
				var b bytes.Buffer
				for i := 0; i < 1001; i++ {
					if tty {
						b.WriteString(line + "\n")
					} else {
						b.Write(frame(1, line+"\n"))
					}
				}
				res, err := decodeLogs(&b, tty, 1000)
				if err != nil || len(res.Lines) != 1000 {
					t.Fatalf("tty=%v: %v %d", tty, err, len(res.Lines))
				}
				return res
			})
			runtime.KeepAlive(keep)
			if grew > limit {
				t.Fatalf("tty=%v bad=%v: result retains %d MiB", tty, bad, grew>>20)
			}
		}
	}
}

// lazyReader serves a header (optional) and then an endless run of fill bytes
// with no newline. It samples the heap as the decoder consumes it and ends
// with an error after budget bytes, so a decoder that buffers without a cap is
// caught by the peak.
type lazyReader struct {
	head   []byte
	fill   byte
	sent   int
	budget int
	peak   uint64
	base   uint64
	err    error
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if len(l.head) > 0 {
		n := copy(p, l.head)
		l.head = l.head[n:]
		return n, nil
	}
	if l.sent >= l.budget {
		return 0, l.err
	}
	if l.sent%(16<<20) < len(p) {
		if h := heapNow(); h > l.base && h-l.base > l.peak {
			l.peak = h - l.base
		}
	}
	for i := range p {
		p[i] = l.fill
	}
	l.sent += len(p)
	return len(p), nil
}

func TestDecodeMemoryBoundWithoutNewlines(t *testing.T) {
	const limit = 16 << 20
	boom := errors.New("budget spent")
	cases := map[string]struct {
		head []byte
		tty  bool
		fill byte
	}{
		// A frame declaring 4 GiB, fed from zeros.
		"multiplexed 0xFFFFFFFF": {head: []byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}, fill: 0},
		"multiplexed stderr":     {head: []byte{2, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}, fill: 'e'},
		// An unterminated raw TTY stream.
		"tty no newline": {tty: true, fill: 'a'},
	}
	for name, tc := range cases {
		l := &lazyReader{head: tc.head, fill: tc.fill, budget: 96 << 20, err: boom, base: heapNow()}
		_, err := decodeLogs(l, tc.tty, 1000)
		if !errors.Is(err, boom) {
			t.Fatalf("%s: %v", name, err)
		}
		if l.sent < l.budget {
			t.Fatalf("%s: stopped early", name)
		}
		if l.peak > limit {
			t.Fatalf("%s: peak heap %d MiB while reading %d MiB", name, l.peak>>20, l.sent>>20)
		}
	}
}
