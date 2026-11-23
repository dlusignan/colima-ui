package dockerc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLogTail is the largest number of lines Logs returns.
	MaxLogTail = 1000
	// maxLogLineRunes cuts one line. The timestamp counts toward it.
	maxLogLineRunes = 4000
	// maxLogFrame is how much of one unfinished line is kept. The rest of the
	// line is read and discarded, so memory stays bounded.
	maxLogFrame = 64 << 10
	// logHeaderLen is the size of a multiplexed frame header.
	logHeaderLen = 8
	// logReadChunk is the size of one read from the body.
	logReadChunk = 32 << 10
)

// LogResult is the decoded tail of one container's output.
type LogResult struct {
	// Lines are UTF-8 clean, carry Docker's timestamps, are cut at 4000
	// characters and are at most the requested tail, oldest first.
	Lines []string
	// Older is true when the container had more entries than were asked for.
	Older bool
	// Cut is true when at least one line was shortened.
	Cut bool
}

// logIDPattern is the id shape Logs accepts, see ValidLogID.
var logIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// errMalformedLogs marks a stream that is not valid Docker log framing. It
// wraps ErrDockerUnavailable, so callers answer 502.
var errMalformedLogs = fmt.Errorf("%w: malformed log stream", ErrDockerUnavailable)

// decodeLogs reads a logs response body and keeps the last tail lines. With
// tty false the body is multiplexed (8 byte header: stream, three zero bytes,
// big-endian size, then the payload). With tty true it is raw text split on
// newlines. Stdout and stderr land in one list in arrival order. The reader's
// own errors are returned unchanged; framing problems return errMalformedLogs.
// Container output is never put in an error.
func decodeLogs(r io.Reader, tty bool, tail int) (LogResult, error) {
	if tail < 1 {
		tail = 1
	}
	b := newLogBuilder(tail)
	var err error
	if tty {
		err = decodeRaw(r, b)
	} else {
		err = decodeMux(r, b)
	}
	if err != nil {
		return LogResult{}, err
	}
	return b.result(), nil
}

func decodeMux(r io.Reader, b *logBuilder) error {
	var hdr [logHeaderLen]byte
	buf := make([]byte, logReadChunk)
	var asm [2]lineAsm // stdout, stderr; each keeps its own unfinished line
	seq := 0
	for {
		n, err := io.ReadFull(r, hdr[:])
		if err != nil {
			if n == 0 && errors.Is(err, io.EOF) {
				flushLines(b, &asm[0], &asm[1]) // clean end at a frame boundary
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return errMalformedLogs
			}
			return err
		}
		if hdr[0] > 3 || hdr[1] != 0 || hdr[2] != 0 || hdr[3] != 0 {
			return errMalformedLogs
		}
		stream := hdr[0]
		// Stream 3 is Docker's error channel: a read failed part way, so the
		// snapshot is incomplete. Its text is never kept.
		if stream == 3 {
			b.cut = true
		}
		for remaining := int64(binary.BigEndian.Uint32(hdr[4:])); remaining > 0; {
			chunk := buf[:min(remaining, int64(len(buf)))]
			if _, err := io.ReadFull(r, chunk); err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
					return errMalformedLogs
				}
				return err
			}
			remaining -= int64(len(chunk))
			if stream == 1 || stream == 2 { // 0 is stdin, 3 is the error channel
				asm[stream-1].feed(chunk, b, &seq)
			}
		}
	}
}

func decodeRaw(r io.Reader, b *logBuilder) error {
	buf := make([]byte, logReadChunk)
	var asm lineAsm
	seq := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			asm.feed(buf[:n], b, &seq)
		}
		switch {
		case err == nil:
		case errors.Is(err, io.EOF):
			flushLines(b, &asm)
			return nil
		default:
			return err
		}
	}
}

// lineAsm builds lines from bytes that arrive in arbitrary pieces. It keeps at
// most maxLogFrame bytes of the current line and drops the rest.
type lineAsm struct {
	buf   []byte
	over  bool
	start int // arrival order of the first byte of the pending line
}

func (a *lineAsm) pending() bool { return len(a.buf) > 0 || a.over }

func (a *lineAsm) take(p []byte, seq *int) {
	if !a.pending() && len(p) > 0 {
		*seq++
		a.start = *seq
	}
	if room := maxLogFrame - len(a.buf); len(p) <= room {
		a.buf = append(a.buf, p...)
	} else {
		a.buf = append(a.buf, p[:room]...)
		a.over = true
	}
}

func (a *lineAsm) feed(p []byte, b *logBuilder, seq *int) {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			a.take(p, seq)
			return
		}
		a.take(p[:i], seq)
		b.add(a.buf, a.over)
		a.buf, a.over = a.buf[:0], false
		p = p[i+1:]
	}
}

// flushLines emits unfinished lines at the end of the stream, oldest first.
func flushLines(b *logBuilder, asm ...*lineAsm) {
	for {
		var first *lineAsm
		for _, a := range asm {
			if a.pending() && (first == nil || a.start < first.start) {
				first = a
			}
		}
		if first == nil {
			return
		}
		b.add(first.buf, first.over)
		first.buf, first.over = nil, false
	}
}

// logBuilder cleans lines and keeps the newest tail+1 in a ring.
type logBuilder struct {
	tail  int
	ring  []string
	start int
	count int
	cut   bool
}

func newLogBuilder(tail int) *logBuilder {
	return &logBuilder{tail: tail, ring: make([]string, tail+1)}
}

// add takes one line without its newline. over means the entry was longer than the bytes given.
func (b *logBuilder) add(raw []byte, over bool) {
	if over {
		b.cut = true
	}
	if n := len(raw); n > 0 && raw[n-1] == '\r' {
		raw = raw[:n-1]
	}
	s := string(raw)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) > maxLogLineRunes { // fewer bytes than the limit cannot exceed it
		runes := 0
		for i := range s {
			if runes == maxLogLineRunes {
				s = s[:i]
				b.cut = true
				break
			}
			runes++
		}
	}
	// The kept string must not share the (up to 64 KiB) buffer it was cut from.
	s = strings.Clone(s)
	if b.count < len(b.ring) {
		b.ring[(b.start+b.count)%len(b.ring)] = s
		b.count++
		return
	}
	b.ring[b.start] = s
	b.start = (b.start + 1) % len(b.ring)
}

func (b *logBuilder) result() LogResult {
	res := LogResult{Cut: b.cut}
	skip := 0
	if b.count > b.tail {
		res.Older = true
		skip = b.count - b.tail
	}
	res.Lines = make([]string, 0, b.count-skip)
	for i := skip; i < b.count; i++ {
		res.Lines = append(res.Lines, b.ring[(b.start+i)%len(b.ring)])
	}
	return res
}

// ValidLogID reports whether id has the only shape Logs accepts: 12 to 64
// lowercase hexadecimal characters, as Docker prints container ids. Names and
// prefixes never reach a Docker URL.
func ValidLogID(id string) bool { return logIDPattern.MatchString(id) }
