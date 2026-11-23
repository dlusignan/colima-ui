package dockerc

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLogTail is the largest number of lines Logs returns.
	MaxLogTail = 1000
	// maxLogLineRunes cuts one line. The timestamp counts toward it.
	maxLogLineRunes = 4000
	// maxLogFrame is how much of one frame (or one raw line) is kept. The
	// rest is read and discarded, so memory stays bounded.
	maxLogFrame = 64 << 10
	// logHeaderLen is the size of a multiplexed frame header.
	logHeaderLen = 8
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
	buf := make([]byte, maxLogFrame)
	for {
		n, err := io.ReadFull(r, hdr[:])
		if err != nil {
			if n == 0 && errors.Is(err, io.EOF) {
				return nil // clean end at a frame boundary
			}
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return errMalformedLogs
			}
			return err
		}
		if hdr[0] > 3 || hdr[1] != 0 || hdr[2] != 0 || hdr[3] != 0 {
			return errMalformedLogs
		}
		size := int64(binary.BigEndian.Uint32(hdr[4:]))
		keep := size
		if keep > maxLogFrame {
			keep = maxLogFrame
		}
		if _, err := io.ReadFull(r, buf[:keep]); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return errMalformedLogs
			}
			return err
		}
		if rest := size - keep; rest > 0 {
			if _, err := io.CopyN(io.Discard, r, rest); err != nil {
				if errors.Is(err, io.EOF) {
					return errMalformedLogs
				}
				return err
			}
		}
		// Stream 0 is stdin and 3 is a Docker error channel: no output.
		if hdr[0] == 1 || hdr[0] == 2 {
			if size == 0 {
				continue
			}
			b.add(buf[:keep], size > keep)
		}
	}
}

func decodeRaw(r io.Reader, b *logBuilder) error {
	br := bufio.NewReaderSize(r, 4096)
	var line []byte
	over := false
	for {
		chunk, err := br.ReadSlice('\n')
		if room := maxLogFrame - len(line); len(chunk) <= room {
			line = append(line, chunk...)
		} else {
			if room > 0 {
				line = append(line, chunk[:room]...)
			}
			over = true
		}
		switch {
		case err == nil:
			b.add(line, over)
			line, over = line[:0], false
		case errors.Is(err, bufio.ErrBufferFull):
			// The line continues in the next chunk.
		case errors.Is(err, io.EOF):
			if len(line) > 0 || over {
				b.add(line, over)
			}
			return nil
		default:
			return err
		}
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

// add takes one entry. over means the entry was longer than the bytes given.
func (b *logBuilder) add(raw []byte, over bool) {
	if over {
		b.cut = true
	}
	if n := len(raw); n > 0 && raw[n-1] == '\n' {
		raw = raw[:n-1]
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
