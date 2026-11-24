package dockerc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Pull limits. A pull is slow, so it has its own deadlines instead of
// writeTimeout.
const (
	pullTimeout     = 30 * time.Minute
	pullIdleTimeout = 2 * time.Minute

	// maxPullLine is the longest progress line that is parsed; a longer one is
	// skipped whole. maxPullStream bounds the bytes read from one pull.
	maxPullLine   = 64 << 10
	maxPullStream = 128 << 20
	// maxPullMessage caps a registry message shown to the user, in runes.
	maxPullMessage = 300
	// maxPullLayers and maxPullLines bound what one pull can make us keep and
	// emit, whatever the stream says.
	maxPullLayers = 256
	maxPullLines  = 3000
)

// ErrPullFailed is the kind of a *PullError whose cause is the registry or the
// pull itself (not found, denied, a broken stream), as opposed to a transport
// failure, which keeps its own sentinel.
var ErrPullFailed = errors.New("dockerc: image pull failed")

// PullError is a pull failure with a message that is safe to show: it came from
// the registry or from Docker's error object, stripped of control characters
// and ANSI sequences, redacted of absolute paths and cut to maxPullMessage
// runes. Error() carries only the fixed kind, so logging an error never prints
// registry text. Kind is a sentinel (ErrObjectNotFound, ErrPullFailed,
// ErrDaemon, ...); Unwrap returns it.
type PullError struct {
	Kind    error
	Message string
}

func (e *PullError) Error() string { return e.Kind.Error() }
func (e *PullError) Unwrap() error { return e.Kind }

// ValidPullRef reports whether ref can be pulled: a valid image reference that
// carries a tag or a digest (so a pull never silently takes "latest") and whose
// repository is not "sha256" or a 64 character hex string (which would read
// like an image ID).
func ValidPullRef(ref string) bool {
	_, _, ok := splitPullRef(ref)
	return ok
}

// splitPullRef returns the fromImage and tag query values for ref. A digest
// reference travels whole in fromImage with no tag parameter (the published
// v1.41 text: "The name may include a tag or digest"); a tag reference splits
// into the repository (registry host and port included) and the tag.
func splitPullRef(ref string) (from, tag string, ok bool) {
	if !ValidImageRef(ref) {
		return "", "", false
	}
	rest := ref
	digest := false
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		rest, digest = rest[:i], true
	}
	if i := strings.LastIndexByte(rest, ':'); i > strings.LastIndexByte(rest, '/') {
		if !digest {
			from, tag = rest[:i], rest[i+1:]
		}
		rest = rest[:i]
	} else if !digest {
		return "", "", false // neither tag nor digest
	}
	if !ValidImageRepository(rest) {
		return "", "", false
	}
	if digest {
		return ref, "", true
	}
	return from, tag, true
}

// PullImage implements Client. See the interface comment. The request is
// POST /images/create with only fromImage and tag; X-Registry-Auth is never
// sent, so only public images can be pulled, and fromSrc, repo, changes and
// platform are never sent, so this call cannot import anything.
func (c *httpClient) PullImage(ctx context.Context, ref string, emit func(string)) error {
	from, tag, ok := splitPullRef(ref)
	if !ok {
		return ErrBadName
	}
	if emit == nil {
		emit = func(string) {}
	}
	q := url.Values{"fromImage": {from}}
	if tag != "" {
		q.Set("tag", tag)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.pullTimeout)
	defer cancel()
	// A pull that goes quiet is cut: a stalled registry must not hold a job
	// (and its limits) forever.
	idle := time.AfterFunc(c.pullIdle, cancel)
	defer idle.Stop()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, "http://docker/"+apiVersion+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("%w: bad request", ErrDockerUnavailable)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.pullTransportErr(ctx, callCtx)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return statusPullError(resp.StatusCode, b)
	}

	body := &idleReader{r: io.LimitReader(resp.Body, maxPullStream), idle: idle, d: c.pullIdle}
	br := bufio.NewReaderSize(body, 4096)
	prog := newPullProgress(emit)
	for {
		line, tooLong, err := readBoundedLine(br, maxPullLine)
		if len(line) > 0 && !tooLong {
			if perr := prog.handle(line); perr != nil {
				return perr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return c.pullTransportErr(ctx, callCtx)
		}
	}
	if ctx.Err() != nil {
		return fmt.Errorf("dockerc: %w", ctx.Err())
	}
	if !prog.finished {
		// Docker always ends a successful pull with a Status line. A stream
		// that just stops is a pull that did not finish.
		return &PullError{Kind: ErrPullFailed, Message: "The pull ended before Docker reported a result."}
	}
	return nil
}

// pullTransportErr maps a failed round trip or read. A caller that cancelled
// keeps its own error; a quiet or over-long pull is a timeout.
func (c *httpClient) pullTransportErr(parent, call context.Context) error {
	if err := parent.Err(); err != nil {
		return fmt.Errorf("dockerc: %w", err)
	}
	if call.Err() != nil {
		return &PullError{Kind: ErrDockerUnavailable, Message: "The pull stopped because Docker went quiet or took too long."}
	}
	return fmt.Errorf("%w: cannot reach docker socket", ErrDockerUnavailable)
}

// statusPullError maps a non-200 answer of the create call. The daemon's
// message field (for a 404, the registry's own text) is shown after cleaning;
// a body that is not the expected JSON gets a fixed text.
func statusPullError(status int, body []byte) error {
	kind := writeStatusErr(status)
	var m struct {
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(body, &m) == nil {
		msg = CleanPullText(m.Message)
	}
	if msg == "" {
		switch status {
		case http.StatusNotFound:
			msg = "The image was not found, or it needs credentials."
		case http.StatusBadRequest:
			msg = "Docker did not accept the image reference."
		default:
			msg = "Docker answered with an error."
		}
	}
	return &PullError{Kind: kind, Message: msg}
}

// idleReader pushes the idle timer back on every successful read.
type idleReader struct {
	r    io.Reader
	idle *time.Timer
	d    time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.idle.Reset(r.d)
	}
	return n, err
}

// readBoundedLine reads one line of at most max bytes. A longer line is read
// to its end and discarded (tooLong). At the end of input it returns the last
// partial line, then io.EOF.
func readBoundedLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, rerr := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case rerr == nil:
			return bytes.TrimRight(line, "\r\n"), tooLong, nil
		case errors.Is(rerr, bufio.ErrBufferFull):
			continue
		default:
			return bytes.TrimRight(line, "\r\n"), tooLong, rerr
		}
	}
}

var (
	ansiCSI = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	ansiOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?")
	// pathLike matches an absolute path of two or more segments, or a unix://
	// socket address, so a registry or daemon message cannot show a host path.
	pathLike  = regexp.MustCompile(`(?:unix://\S+|(?:^|[\s"'(=:\[])(?:~|\.\.?)?(?:/[^\s/"')\]:,;]+){2,}/?)`)
	winPath   = regexp.MustCompile(`[A-Za-z]:\\\S+`)
	layerID   = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
	digestRe  = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_+.][a-z][a-z0-9]*)*:[0-9a-f]{32,128}$`)
	spaceRuns = regexp.MustCompile(`\s+`)
)

// CleanPullText makes registry or daemon text safe to show in a job's output:
// ANSI escape sequences are removed, every character that is not printable
// (controls, format and bidi characters, invalid UTF-8) is dropped, whitespace
// collapses to single spaces, absolute paths and socket addresses become
// "[path]", text that looks like raw JSON becomes empty, and the result is cut
// to 300 runes. It is idempotent. An empty result means "nothing safe to show".
func CleanPullText(s string) string {
	if len(s) > 8*maxPullMessage*4 {
		s = s[:8*maxPullMessage*4] // bound the work on hostile input
	}
	s = ansiOSC.ReplaceAllString(ansiCSI.ReplaceAllString(s, ""), "")
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		switch {
		case r == utf8.RuneError && n <= 1:
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case unicode.IsPrint(r):
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(spaceRuns.ReplaceAllString(b.String(), " "))
	out = pathLike.ReplaceAllStringFunc(out, func(m string) string {
		if strings.HasPrefix(m, "unix://") {
			return "[path]"
		}
		// Keep the delimiter that opened the match.
		if c := m[0]; c != '/' && c != '~' && c != '.' {
			return string(c) + "[path]"
		}
		return "[path]"
	})
	out = winPath.ReplaceAllString(out, "[path]")
	if strings.HasPrefix(out, "{") || strings.HasPrefix(out, "[{") || strings.HasPrefix(out, "[\"") {
		return ""
	}
	if r := []rune(out); len(r) > maxPullMessage {
		out = string(r[:maxPullMessage-1]) + "…"
	}
	return out
}

// pullMsg is one object of Docker's progress stream. Only these fields are
// decoded; everything else (progress bars, aux, timestamps) is ignored.
type pullMsg struct {
	Status      string `json:"status"`
	ID          string `json:"id"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	ProgressDetail *struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
}

// Layer states, in the order a layer moves through them.
const (
	lsNone = iota
	lsQueued
	lsDownloading
	lsDownloaded
	lsExtracting
	lsDone
)

type layer struct {
	state     int
	announced int // download milestones already reported, 0 to 4
}

// pullProgress turns the stream into compact lines: one per layer state change
// and a few download milestones per layer, never one per byte tick.
type pullProgress struct {
	emit     func(string)
	layers   map[string]*layer
	lines    int
	finished bool
}

func newPullProgress(emit func(string)) *pullProgress {
	return &pullProgress{emit: emit, layers: map[string]*layer{}}
}

func (p *pullProgress) out(s string) {
	if p.lines >= maxPullLines {
		return
	}
	p.lines++
	p.emit(s)
}

// handle processes one raw line. A line that is not the expected JSON is
// skipped and never echoed. It returns a *PullError when the stream carries an
// error object (Docker reports pull failures that way inside a 200 answer).
func (p *pullProgress) handle(raw []byte) error {
	var m pullMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	if msg := m.Error; msg != "" || m.ErrorDetail != nil {
		if m.ErrorDetail != nil && m.ErrorDetail.Message != "" {
			msg = m.ErrorDetail.Message
		}
		text := CleanPullText(msg)
		if text == "" {
			text = "The registry reported an error."
		}
		return &PullError{Kind: classifyPullMessage(text), Message: text}
	}
	status := m.Status
	switch {
	case strings.HasPrefix(status, "Pulling from "):
		if t := CleanPullText(status); t != "" {
			p.out(t)
		}
		return nil
	case strings.HasPrefix(status, "Digest: "):
		if d := strings.TrimPrefix(status, "Digest: "); digestRe.MatchString(d) {
			p.out("Digest: " + d)
		}
		return nil
	case strings.HasPrefix(status, "Status: "):
		p.finished = true
		switch rest := strings.TrimPrefix(status, "Status: "); {
		case strings.HasPrefix(rest, "Image is up to date"):
			p.out("Status: image is up to date, nothing was downloaded")
		case strings.HasPrefix(rest, "Downloaded newer image"):
			p.out("Status: downloaded newer image")
		default:
			if t := CleanPullText(status); t != "" {
				p.out(t)
			}
		}
		return nil
	}
	if layerID.MatchString(m.ID) {
		p.layerEvent(m)
	}
	return nil
}

// classifyPullMessage marks a not-found style message so callers can tell it
// from other failures. The text itself is shown either way.
func classifyPullMessage(text string) error {
	l := strings.ToLower(text)
	if strings.Contains(l, "not found") || strings.Contains(l, "manifest unknown") || strings.Contains(l, "does not exist") {
		return fmt.Errorf("%w: %w", ErrPullFailed, ErrObjectNotFound)
	}
	return ErrPullFailed
}

func (p *pullProgress) layerEvent(m pullMsg) {
	id := m.ID[:12]
	l := p.layers[id]
	if l == nil {
		if len(p.layers) >= maxPullLayers {
			return
		}
		l = &layer{}
		p.layers[id] = l
	}
	advance := func(to int, text string) {
		if to > l.state {
			l.state = to
			p.out(id + ": " + text)
		}
	}
	switch strings.ToLower(m.Status) {
	case "pulling fs layer", "waiting":
		advance(lsQueued, "waiting")
	case "downloading":
		if l.state < lsDownloading {
			text := "downloading"
			if m.ProgressDetail != nil && m.ProgressDetail.Total > 0 {
				text += " (" + humanBytes(m.ProgressDetail.Total) + ")"
			}
			advance(lsDownloading, text)
		}
		if l.state == lsDownloading && m.ProgressDetail != nil && m.ProgressDetail.Total > 0 && m.ProgressDetail.Current >= 0 {
			// Milestones at 25, 50 and 75 percent: bounded, unlike byte ticks.
			pct := int(min(m.ProgressDetail.Current, m.ProgressDetail.Total) * 100 / m.ProgressDetail.Total)
			for l.announced < 3 && pct >= (l.announced+1)*25 {
				l.announced++
				p.out(id + ": downloading " + strconv.Itoa(l.announced*25) + "%")
			}
		}
	case "download complete":
		advance(lsDownloaded, "download complete")
	case "extracting":
		advance(lsExtracting, "extracting")
	case "pull complete":
		advance(lsDone, "pull complete")
	case "already exists":
		advance(lsDone, "already exists")
	}
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("kMGTP"[exp]) + "B"
}
