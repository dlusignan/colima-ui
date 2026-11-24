package dockerc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// writeTimeout is the default deadline of one write call. Pull and other
// slow calls pass their own.
const writeTimeout = 30 * time.Second

// errUnusableAnswer marks a success status whose body was too large or not the
// expected JSON. It is always wrapped with ErrDockerUnavailable. A caller whose
// write is irreversible and has already happened (prune) uses it to tell "Docker
// did it but the answer is unreadable" from "could not reach Docker".
var errUnusableAnswer = errors.New("dockerc: unusable answer")

// fixedPath is a constant Docker URL path such as "/volumes/create". The
// named type makes a caller write fixedPath("...") or pass an untyped constant;
// a validated name must go through objectTarget or refTarget instead.
type fixedPath string

// target is the URL path of one write call. It can only be made by
// fixedTarget, objectTarget or refTarget, so the write helpers never receive
// a path string a caller assembled from a name: a name or reference is
// validated and escaped as one unit, and cannot add a segment.
type target struct{ path string }

// fixedTarget is a target for a constant path with no variable part.
func fixedTarget(p fixedPath) target { return target{path: string(p)} }

// objectTarget is root + "/" + name, for example objectTarget("/volumes",
// "pgdata"), optionally followed by constant suffixes such as "/disconnect".
// It fails with ErrBadName unless name passes ValidObjectName.
func objectTarget(root fixedPath, name string, suffix ...fixedPath) (target, error) {
	p, err := ObjectPath(string(root), name)
	if err != nil {
		return target{}, err
	}
	return target{path: p + joinSuffix(suffix)}, nil
}

// refTarget is objectTarget for an image reference (ValidImageRef).
func refTarget(root fixedPath, ref string, suffix ...fixedPath) (target, error) {
	p, err := RefPath(string(root), ref)
	if err != nil {
		return target{}, err
	}
	return target{path: p + joinSuffix(suffix)}, nil
}

func joinSuffix(s []fixedPath) string {
	var b strings.Builder
	for _, x := range s {
		b.WriteString(string(x))
	}
	return b.String()
}

// post performs one POST with a deadline. The target comes from fixedTarget, objectTarget or refTarget. A non-nil body is sent as JSON; a
// non-nil dst receives the size-capped JSON answer. A status in ok counts as
// success (200, 201 and 204 when ok is empty); any other status is mapped by
// writeStatusErr, and the daemon's error text is dropped.
func (c *httpClient) post(ctx context.Context, timeout time.Duration, t target, query url.Values, body, dst any, ok ...int) error {
	_, err := c.send(ctx, http.MethodPost, timeout, t.path, query, body, dst, ok)
	return err
}

// postStatus is post for a call whose success statuses mean different things
// (204 done, 304 already in that state). It returns the status that was
// accepted; on an error the status is 0.
func (c *httpClient) postStatus(ctx context.Context, timeout time.Duration, t target, query url.Values, ok ...int) (int, error) {
	return c.send(ctx, http.MethodPost, timeout, t.path, query, nil, nil, ok)
}

// delete performs one DELETE with a deadline, with the same rules as post.
func (c *httpClient) delete(ctx context.Context, timeout time.Duration, t target, query url.Values, dst any, ok ...int) error {
	_, err := c.send(ctx, http.MethodDelete, timeout, t.path, query, nil, dst, ok)
	return err
}

func (c *httpClient) send(ctx context.Context, method string, timeout time.Duration, path string, query url.Values, body, dst any, ok []int) (int, error) {
	if !safeURLPath(path) {
		return 0, ErrBadName
	}
	if timeout <= 0 {
		timeout = writeTimeout
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("%w: bad request body", ErrInvalidRequest)
		}
		rdr = bytes.NewReader(b)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := "http://docker/" + apiVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(callCtx, method, u, rdr)
	if err != nil {
		return 0, fmt.Errorf("%w: bad request", ErrDockerUnavailable)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, c.transportErr(ctx, callCtx)
	}
	defer resp.Body.Close()
	if !statusOK(resp.StatusCode, ok) {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0, writeStatusErr(resp.StatusCode)
	}
	if dst == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return 0, c.transportErr(ctx, callCtx)
	}
	if int64(len(raw)) > c.maxBody {
		return 0, fmt.Errorf("%w: response too large: %w", ErrDockerUnavailable, errUnusableAnswer)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return 0, fmt.Errorf("%w: malformed response: %w", ErrDockerUnavailable, errUnusableAnswer)
	}
	return resp.StatusCode, nil
}

func statusOK(status int, ok []int) bool {
	if len(ok) == 0 {
		return status == http.StatusOK || status == http.StatusCreated || status == http.StatusNoContent
	}
	for _, s := range ok {
		if s == status {
			return true
		}
	}
	return false
}

// writeStatusErr maps a Docker status of a write call to a typed error. 403
// is Docker's answer for a predefined object (the bridge network), so it is a
// conflict. Only the status number is kept.
func writeStatusErr(status int) error {
	switch status {
	case http.StatusNotFound:
		return ErrObjectNotFound
	case http.StatusConflict, http.StatusForbidden:
		return ErrConflict
	case http.StatusBadRequest:
		return ErrInvalidRequest
	}
	return fmt.Errorf("%w: status %d", ErrDaemon, status)
}

// safeURLPath is the last defence of the write helpers: path must start with
// "/" and every segment, once unescaped, must be non-empty, not "." or "..",
// and free of control characters, "?" and "#". Names are validated before
// this, so a failure here is a bug in a caller, not user input.
func safeURLPath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for _, seg := range strings.Split(path[1:], "/") {
		s, err := url.PathUnescape(seg)
		if err != nil || s == "" || s == "." || s == ".." {
			return false
		}
		for _, r := range s {
			if r < 0x20 || r == 0x7f || r == '?' || r == '#' || r == '\\' {
				return false
			}
		}
	}
	return true
}
