package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// APIError is an error with the contract's wire shape (docs/api.md section 2).
// Message is text for people and must never carry request-controlled values
// or internal error text. Details may carry only validated values.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return fmt.Sprintf("api error %d %s", e.Status, e.Code) }

// errorBody is the wire form: {"error":{"code","message","details"}}.
type errorBody struct {
	Error errorFields `json:"error"`
}

type errorFields struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// writeAPIError writes e in the contract shape. Nil Details encode as {}.
func writeAPIError(w http.ResponseWriter, e *APIError) {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, e.Status, errorBody{Error: errorFields{Code: e.Code, Message: e.Message, Details: details}})
}

// apiHandler is a handler that returns its failure. A returned *APIError is
// written as is; any other error becomes a generic 500 and is logged here,
// never sent to the client.
type apiHandler func(http.ResponseWriter, *http.Request) error

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tw := &trackWriter{ResponseWriter: w}
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				panic(p)
			}
			slog.Error("api handler panicked", "method", r.Method, "panic", p)
			if !tw.wrote {
				writeAPIError(w, errInternal())
			}
		}
	}()
	w = tw
	err := h(w, r)
	if err == nil {
		return
	}
	var ae *APIError
	if errors.As(err, &ae) {
		writeAPIError(w, ae)
		return
	}
	slog.Error("api handler failed", "method", r.Method, "error", err)
	writeAPIError(w, errInternal())
}

// trackWriter records whether a response has started, so the panic recovery
// does not write a second header. It forwards Flush for streaming handlers.
type trackWriter struct {
	http.ResponseWriter
	wrote bool
}

func (t *trackWriter) WriteHeader(code int) { t.wrote = true; t.ResponseWriter.WriteHeader(code) }

func (t *trackWriter) Write(b []byte) (int, error) { t.wrote = true; return t.ResponseWriter.Write(b) }

func (t *trackWriter) Flush() {
	t.wrote = true
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *trackWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

func newAPIError(status int, code, message string, details map[string]any) *APIError {
	return &APIError{Status: status, Code: code, Message: message, Details: details}
}

func errBadRequest(message string) *APIError {
	return newAPIError(http.StatusBadRequest, "bad_request", message, nil)
}

func errNotFound() *APIError {
	return newAPIError(http.StatusNotFound, "not_found", "No such route.", nil)
}

func errMethodNotAllowed() *APIError {
	return newAPIError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed for this route.", nil)
}

func errInternal() *APIError {
	return newAPIError(http.StatusInternalServerError, "internal_error", "Internal error.", nil)
}

func errColimaFailed() *APIError {
	return newAPIError(http.StatusBadGateway, "colima_failed", "Colima could not be run or returned an error.", nil)
}

// defaultMessages are the fixed texts for errors raised outside handlers
// (middleware, static files) through writeError.
var defaultMessages = map[string]string{
	"unauthorized":       "A valid bearer token is required.",
	"forbidden_host":     "Host not allowed.",
	"forbidden_origin":   "Origin not allowed.",
	"not_found":          "No such route.",
	"method_not_allowed": "Method not allowed for this route.",
}
