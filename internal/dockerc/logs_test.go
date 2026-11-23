package dockerc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

func frame(stream byte, payload string) []byte {
	h := make([]byte, 8, 8+len(payload))
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func mux(frames ...[]byte) []byte { return bytes.Join(frames, nil) }

// checkInvariants holds for any input, valid or not.
func checkInvariants(t testing.TB, res LogResult, tail int) {
	t.Helper()
	if len(res.Lines) > tail {
		t.Fatalf("%d lines for tail %d", len(res.Lines), tail)
	}
	for _, l := range res.Lines {
		if !utf8.ValidString(l) {
			t.Fatalf("invalid UTF-8 line %q", l)
		}
		if n := utf8.RuneCountInString(l); n > maxLogLineRunes {
			t.Fatalf("line of %d runes", n)
		}
		if strings.ContainsAny(l, "\n") {
			t.Fatalf("line contains a newline: %q", l)
		}
	}
}

// splitReader returns the data in two reads, cut at pos.
type splitReader struct {
	data []byte
	pos  int
	done int
}

func (s *splitReader) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	limit := len(s.data)
	if s.done == 0 && s.pos > 0 && s.pos < len(s.data) {
		limit = s.pos
	}
	n := copy(p, s.data[:min(limit, len(p))])
	s.data = s.data[n:]
	s.done++
	return n, nil
}

func TestDecodeMuxMergesInArrivalOrder(t *testing.T) {
	body := mux(
		frame(1, "2026-05-02T09:41:07.000000001Z one\n"),
		frame(2, "2026-05-02T09:41:07.000000002Z err\n"),
		frame(1, "2026-05-02T09:41:07.000000003Z two\r\n"),
	)
	res, err := decodeLogs(bytes.NewReader(body), false, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"2026-05-02T09:41:07.000000001Z one",
		"2026-05-02T09:41:07.000000002Z err",
		"2026-05-02T09:41:07.000000003Z two",
	}
	if fmt.Sprint(res.Lines) != fmt.Sprint(want) || res.Older || res.Cut {
		t.Fatalf("%+v", res)
	}
}

func TestDecodeMuxSplitAtEveryByteBoundary(t *testing.T) {
	body := mux(
		frame(1, "a first line\n"),
		frame(2, "b second\n"),
		frame(1, "c héllo wörld\n"),
	)
	want := []string{"a first line", "b second", "c héllo wörld"}
	for pos := 0; pos <= len(body); pos++ {
		res, err := decodeLogs(&splitReader{data: body, pos: pos}, false, 10)
		if err != nil || fmt.Sprint(res.Lines) != fmt.Sprint(want) {
			t.Fatalf("split at %d: %v %+v", pos, err, res)
		}
	}
	for name, r := range map[string]io.Reader{
		"one byte":  iotest.OneByteReader(bytes.NewReader(body)),
		"half":      iotest.HalfReader(bytes.NewReader(body)),
		"data err":  iotest.DataErrReader(bytes.NewReader(body)),
		"timeouter": iotest.OneByteReader(iotest.DataErrReader(bytes.NewReader(body))),
	} {
		res, err := decodeLogs(r, false, 10)
		if err != nil || fmt.Sprint(res.Lines) != fmt.Sprint(want) {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
	}
}

func TestDecodeMuxTailAndOlder(t *testing.T) {
	var frames [][]byte
	for i := 1; i <= 5; i++ {
		frames = append(frames, frame(1, fmt.Sprintf("line %d\n", i)))
	}
	body := mux(frames...)
	for _, tc := range []struct {
		tail  int
		want  string
		older bool
	}{
		{5, "[line 1 line 2 line 3 line 4 line 5]", false},
		{6, "[line 1 line 2 line 3 line 4 line 5]", false},
		{4, "[line 2 line 3 line 4 line 5]", true},
		{1, "[line 5]", true},
		{0, "[line 5]", true}, // below 1 is treated as 1
	} {
		res, err := decodeLogs(bytes.NewReader(body), false, tc.tail)
		if err != nil || fmt.Sprint(res.Lines) != tc.want || res.Older != tc.older {
			t.Fatalf("tail %d: %v %+v", tc.tail, err, res)
		}
	}
}

func TestDecodeMuxSkipsStdinAndEmptyFrames(t *testing.T) {
	body := mux(frame(0, "stdin\n"), frame(1, ""), frame(2, "kept\n"))
	res, err := decodeLogs(bytes.NewReader(body), false, 10)
	if err != nil || fmt.Sprint(res.Lines) != "[kept]" || res.Cut {
		t.Fatalf("%v %+v", err, res)
	}
}

// A frame on Docker's error channel (stream 3) means a read failed part way.
// The snapshot is flagged as truncated and the text never appears.
func TestDecodeMuxErrorChannelMarksTruncated(t *testing.T) {
	body := mux(frame(1, "a\n"), frame(3, "SECRET read error\n"), frame(1, "b\n"))
	res, err := decodeLogs(bytes.NewReader(body), false, 10)
	if err != nil || fmt.Sprint(res.Lines) != "[a b]" || !res.Cut {
		t.Fatalf("%v %+v", err, res)
	}
}

// One frame can hold several lines, and a line can span frames of one stream.
func TestDecodeMuxSplitsOnNewlines(t *testing.T) {
	cases := map[string]struct {
		body []byte
		want string
	}{
		"two lines in one frame":    {mux(frame(1, "a\nb\n")), "[a b]"},
		"blank lines":               {mux(frame(1, "\n\n")), "[ ]"},
		"no trailing newline":       {mux(frame(1, "a\nb")), "[a b]"},
		"line spans frames":         {mux(frame(1, "he"), frame(1, "llo\n")), "[hello]"},
		"spans around other stream": {mux(frame(1, "he"), frame(2, "err\n"), frame(1, "llo\n")), "[err hello]"},
		"unfinished lines flush":    {mux(frame(1, "out"), frame(2, "err")), "[out err]"},
		"unfinished, stderr first":  {mux(frame(2, "err"), frame(1, "out")), "[err out]"},
		"crlf inside a frame":       {mux(frame(1, "a\r\nb\r\n")), "[a b]"},
	}
	for name, tc := range cases {
		res, err := decodeLogs(bytes.NewReader(tc.body), false, 10)
		if err != nil || fmt.Sprint(res.Lines) != tc.want {
			t.Fatalf("%s: %v %q", name, err, res.Lines)
		}
		for pos := 0; pos <= len(tc.body); pos++ {
			res, err := decodeLogs(&splitReader{data: tc.body, pos: pos}, false, 10)
			if err != nil || fmt.Sprint(res.Lines) != tc.want {
				t.Fatalf("%s split %d: %v %q", name, pos, err, res.Lines)
			}
		}
	}
}

func TestDecodeMuxMalformed(t *testing.T) {
	good := frame(1, "hello\n")
	cases := map[string][]byte{
		"stream 4":           mux(good, frame(4, "x\n")),
		"stream 255":         frame(255, "x\n"),
		"padding byte 1":     mux([]byte{1, 1, 0, 0, 0, 0, 0, 1, 'x'}),
		"padding byte 2":     mux([]byte{1, 0, 1, 0, 0, 0, 0, 1, 'x'}),
		"padding byte 3":     mux([]byte{1, 0, 0, 1, 0, 0, 0, 1, 'x'}),
		"raw text as mux":    []byte("2026-05-02T09:41:07Z not framed\n"),
		"eof in header":      mux(good, good[:5]),
		"eof after 1 byte":   mux(good, []byte{1}),
		"eof in payload":     mux(good, frame(1, "truncated\n")[:12]),
		"eof, no payload":    mux(good, frame(1, "truncated\n")[:8]),
		"huge size, no body": {1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff},
		"huge size, some":    append([]byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}, bytes.Repeat([]byte("x"), 100000)...),
		"discard eof":        append([]byte{1, 0, 0, 0, 0, 2, 0, 0}, bytes.Repeat([]byte("x"), 70000)...),
	}
	for name, body := range cases {
		_, err := decodeLogs(bytes.NewReader(body), false, 10)
		if !errors.Is(err, ErrDockerUnavailable) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestDecodeMuxEmptyAndCleanEOF(t *testing.T) {
	for _, tty := range []bool{false, true} {
		res, err := decodeLogs(bytes.NewReader(nil), tty, 10)
		if err != nil || res.Lines == nil || len(res.Lines) != 0 || res.Older || res.Cut {
			t.Fatalf("tty=%v: %v %+v", tty, err, res)
		}
	}
}

func TestDecodeReaderErrorIsNotMalformed(t *testing.T) {
	boom := errors.New("boom")
	for _, tty := range []bool{false, true} {
		r := io.MultiReader(bytes.NewReader(frame(1, "ok\n")[:10]), iotest.ErrReader(boom))
		_, err := decodeLogs(r, tty, 10)
		if !errors.Is(err, boom) {
			t.Fatalf("tty=%v: %v", tty, err)
		}
	}
	// An error at a frame boundary and inside a discard also pass through.
	_, err := decodeLogs(io.MultiReader(bytes.NewReader(frame(1, "ok\n")), iotest.ErrReader(boom)), false, 10)
	if !errors.Is(err, boom) {
		t.Fatalf("boundary: %v", err)
	}
	big := append([]byte{1, 0, 0, 0, 0, 2, 0, 0}, bytes.Repeat([]byte("x"), maxLogFrame)...)
	_, err = decodeLogs(io.MultiReader(bytes.NewReader(big), iotest.ErrReader(boom)), false, 10)
	if !errors.Is(err, boom) {
		t.Fatalf("discard: %v", err)
	}
}

func TestDecodeInvalidUTF8(t *testing.T) {
	bad := "caf\xc3 \xff\xfe broken \xe2\x82 end"
	for _, tty := range []bool{false, true} {
		var body []byte
		if tty {
			body = []byte(bad + "\n")
		} else {
			body = frame(2, bad+"\n")
		}
		res, err := decodeLogs(bytes.NewReader(body), tty, 10)
		if err != nil || len(res.Lines) != 1 || !utf8.ValidString(res.Lines[0]) || !strings.Contains(res.Lines[0], "�") {
			t.Fatalf("tty=%v: %v %q", tty, err, res.Lines)
		}
		if !strings.HasSuffix(res.Lines[0], " end") {
			t.Fatalf("lost text: %q", res.Lines[0])
		}
	}
}

func TestDecodeCutsAtRunesNotBytes(t *testing.T) {
	// 4000 three-byte runes fit; one more is cut off whole.
	exact := strings.Repeat("€", maxLogLineRunes)
	res, err := decodeLogs(bytes.NewReader(frame(1, exact+"\n")), false, 10)
	if err != nil || res.Lines[0] != exact || res.Cut {
		t.Fatalf("exact: %v cut=%v len=%d", err, res.Cut, len(res.Lines[0]))
	}
	res, err = decodeLogs(bytes.NewReader(frame(1, exact+"€\n")), false, 10)
	if err != nil || res.Lines[0] != exact || !res.Cut {
		t.Fatalf("over: %v cut=%v len=%d", err, res.Cut, len(res.Lines[0]))
	}
	// 4-byte runes and a frame whose bytes exceed 4000 but runes do not.
	res, _ = decodeLogs(bytes.NewReader(frame(1, strings.Repeat("😀", 5000))), false, 10)
	if utf8.RuneCountInString(res.Lines[0]) != maxLogLineRunes || !res.Cut {
		t.Fatalf("emoji: %d", utf8.RuneCountInString(res.Lines[0]))
	}
}

func TestDecodeOversizedFrameIsBoundedAndCut(t *testing.T) {
	size := 3 << 20
	body := mux(
		frame(1, "before\n"),
		frame(1, strings.Repeat("x", size)+"\n"),
		frame(2, "after\n"),
	)
	res, err := decodeLogs(bytes.NewReader(body), false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 3 || res.Lines[0] != "before" || res.Lines[2] != "after" || !res.Cut {
		t.Fatalf("lines=%d cut=%v", len(res.Lines), res.Cut)
	}
	if n := len(res.Lines[1]); n != maxLogLineRunes {
		t.Fatalf("middle line %d", n)
	}
}

func TestDecodeOversizedFrameJustOverCapMarksCut(t *testing.T) {
	// Short enough content, but the frame exceeds the keep cap: the cut flag
	// must be set even though the kept bytes end in a newline.
	payload := strings.Repeat(" ", maxLogFrame) + "tail\n"
	res, err := decodeLogs(bytes.NewReader(frame(1, payload)), false, 10)
	if err != nil || !res.Cut {
		t.Fatalf("%v %+v", err, res.Cut)
	}
}

func TestDecodeMuxManyTinyFramesKeepsRingBounded(t *testing.T) {
	var b bytes.Buffer
	for i := 0; i < 20000; i++ {
		b.Write(frame(byte(1+i%2), fmt.Sprintf("n%d\n", i)))
	}
	res, err := decodeLogs(&b, false, 1000)
	if err != nil || len(res.Lines) != 1000 || !res.Older {
		t.Fatalf("%v %d older=%v", err, len(res.Lines), res.Older)
	}
	if res.Lines[0] != "n19000" || res.Lines[999] != "n19999" {
		t.Fatalf("window %q..%q", res.Lines[0], res.Lines[999])
	}
}

func TestDecodeTTYRaw(t *testing.T) {
	cases := []struct {
		name, in, want string
		older, cut     bool
		tail           int
	}{
		{"lines", "a\nb\nc\n", "[a b c]", false, false, 10},
		{"crlf", "a\r\nb\r\n", "[a b]", false, false, 10},
		{"no trailing newline", "a\nb", "[a b]", false, false, 10},
		{"blank lines kept", "a\n\nb\n", "[a  b]", false, false, 10},
		{"tail", "1\n2\n3\n4\n", "[3 4]", true, false, 2},
		{"only newline", "\n", "[]", false, false, 10},
		{"lone cr", "a\r", "[a]", false, false, 10},
	}
	for _, tc := range cases {
		for _, rd := range []func(io.Reader) io.Reader{
			func(r io.Reader) io.Reader { return r },
			iotest.OneByteReader,
		} {
			res, err := decodeLogs(rd(strings.NewReader(tc.in)), true, tc.tail)
			if err != nil || fmt.Sprint(res.Lines) != tc.want || res.Older != tc.older || res.Cut != tc.cut {
				t.Fatalf("%s: %v %q older=%v cut=%v", tc.name, err, res.Lines, res.Older, res.Cut)
			}
		}
	}
}

func TestDecodeTTYSplitAtEveryByteBoundary(t *testing.T) {
	body := []byte("2026-05-02T09:41:07Z héllo\r\n2026-05-02T09:41:08Z wörld\n")
	for pos := 0; pos <= len(body); pos++ {
		res, err := decodeLogs(&splitReader{data: body, pos: pos}, true, 10)
		if err != nil || len(res.Lines) != 2 || res.Lines[1] != "2026-05-02T09:41:08Z wörld" {
			t.Fatalf("split at %d: %v %q", pos, err, res.Lines)
		}
	}
}

func TestDecodeTTYHugeLineIsBounded(t *testing.T) {
	// 5 MiB without a newline, then a normal line: the first is cut, the
	// second is intact, and nothing is retained beyond the caps.
	body := strings.Repeat("y", 5<<20) + "\nnext\n"
	res, err := decodeLogs(strings.NewReader(body), true, 10)
	if err != nil || len(res.Lines) != 2 || !res.Cut || len(res.Lines[0]) != maxLogLineRunes || res.Lines[1] != "next" {
		t.Fatalf("%v %d cut=%v", err, len(res.Lines), res.Cut)
	}
	// Unterminated huge final line.
	res, err = decodeLogs(strings.NewReader(strings.Repeat("z", 1<<20)), true, 10)
	if err != nil || len(res.Lines) != 1 || !res.Cut {
		t.Fatalf("%v %d cut=%v", err, len(res.Lines), res.Cut)
	}
}

func TestDecodeTTYBinaryGarbage(t *testing.T) {
	// A TTY stream starting like a mux header is still just text.
	res, err := decodeLogs(bytes.NewReader([]byte{1, 0, 0, 0, 0, 0, 0, 5, 'h', 'i', '\n', 0xff, 0x00, '\n'}), true, 10)
	if err != nil || len(res.Lines) != 2 {
		t.Fatalf("%v %q", err, res.Lines)
	}
	checkInvariants(t, res, 10)
}

// fuzzSeeds double as table cases for the plain test below.
var fuzzSeeds = [][]byte{
	nil,
	{},
	{1},
	{1, 0, 0, 0},
	{1, 0, 0, 0, 0, 0, 0, 0},
	{1, 0, 0, 0, 0, 0, 0, 1},
	{1, 0, 0, 0, 0, 0, 0, 1, 'x'},
	{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 'x'},
	{2, 0, 0, 0, 0, 0, 0, 3, 0xff, 0xfe, '\n'},
	{3, 0, 0, 0, 0, 0, 0, 1, 'x', 0, 0, 0, 0, 0, 0, 0},
	{1, 0, 0, 0, 0, 0, 0, 2, '\n', '\n'},
	{4, 0, 0, 0, 0, 0, 0, 1, 'x'},
	{1, 1, 0, 0, 0, 0, 0, 1, 'x'},
	[]byte("plain text\r\nsecond\n"),
	[]byte("\r\n\r\n\n"),
	[]byte("\xff\xfe\xfd"),
	mux(frame(1, "a\n"), frame(2, "b\n")),
	mux(frame(1, strings.Repeat("é", 5000))),
	append(mux(frame(1, "a\n")), 1, 0, 0),
}

func TestDecodeSeedsAndRandomBytes(t *testing.T) {
	check := func(data []byte) {
		for _, tty := range []bool{false, true} {
			for _, tail := range []int{1, 3, 1000} {
				res, err := decodeLogs(bytes.NewReader(data), tty, tail)
				if err != nil {
					if !errors.Is(err, ErrDockerUnavailable) {
						t.Fatalf("unexpected error type %v for %x", err, data)
					}
					continue
				}
				checkInvariants(t, res, tail)
			}
		}
	}
	for _, s := range fuzzSeeds {
		check(s)
	}
	rng := rand.New(rand.NewSource(86))
	for i := 0; i < 3000; i++ {
		data := make([]byte, rng.Intn(200))
		rng.Read(data)
		if i%2 == 0 && len(data) >= 8 { // make headers plausible
			data[0] = byte(rng.Intn(4))
			data[1], data[2], data[3] = 0, 0, 0
			data[4], data[5], data[6] = 0, 0, 0
			data[7] = byte(rng.Intn(len(data)))
		}
		check(data)
	}
}

func TestErrorsNeverCarryOutput(t *testing.T) {
	secret := "SECRET-TOKEN-VALUE"
	for _, body := range [][]byte{
		mux(frame(1, secret+"\n"), []byte{9, 0, 0, 0, 0, 0, 0, 1, 'x'}),
		mux(frame(1, secret+"\n"), frame(1, secret)[:12]),
	} {
		_, err := decodeLogs(bytes.NewReader(body), false, 10)
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%v", err)
		}
	}
}

func FuzzDecodeLogs(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, false, 3)
		f.Add(s, true, 3)
	}
	f.Fuzz(func(t *testing.T, data []byte, tty bool, tail int) {
		if tail < 1 || tail > MaxLogTail {
			tail = 5
		}
		res, err := decodeLogs(bytes.NewReader(data), tty, tail)
		if err != nil {
			if !errors.Is(err, ErrDockerUnavailable) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		checkInvariants(t, res, tail)
	})
}
