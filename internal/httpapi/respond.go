package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const (
	// defaultBodyLimit bounds a decoded request body.
	defaultBodyLimit int64 = 1 << 20
	// emptyBodyLimit bounds the body of action routes, which take none.
	emptyBodyLimit int64 = 1 << 10
)

// writeJSON writes v as JSON. A failed marshal becomes a generic 500 so a
// half-written body never reaches the client.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b = []byte(`{"error":{"code":"internal_error","message":"Internal error.","details":{}}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// decodeBody reads one JSON value into dst. The body is limited (defaultBodyLimit
// unless limit is given), unknown fields are rejected and trailing data is an
// error. Every failure is a 400 bad_request with a fixed message.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any, limit ...int64) error {
	max := defaultBodyLimit
	if len(limit) > 0 {
		max = limit[0]
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errBadRequest("Request body is not valid.")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errBadRequest("Request body is not valid.")
	}
	return nil
}

// emptyBody accepts an empty body or {} (with optional whitespace) and rejects
// everything else, so action routes cannot be used to smuggle input.
func emptyBody(w http.ResponseWriter, r *http.Request) error {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, emptyBodyLimit))
	if err != nil {
		return errBadRequest("Request body is not valid.")
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "{}" {
		return nil
	}
	return errBadRequest("This route takes no request body.")
}

// emptyBodyOnly is emptyBody without the "{}" allowance: a route whose request
// carries no data at all takes an empty body and nothing else.
func emptyBodyOnly(w http.ResponseWriter, r *http.Request) error {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, emptyBodyLimit))
	if err != nil {
		return errBadRequest("Request body is not valid.")
	}
	if len(bytes.TrimSpace(b)) != 0 {
		return errBadRequest("This route takes no request body.")
	}
	return nil
}
