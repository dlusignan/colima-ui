package httpapi

import "net/http"

// writeError writes a contract-shaped error with a fixed message for code. It
// never echoes any request header or value. Middleware and static handlers use
// it; route handlers return *APIError instead.
func writeError(w http.ResponseWriter, status int, code string) {
	msg, ok := defaultMessages[code]
	if !ok {
		msg = http.StatusText(status)
	}
	writeAPIError(w, &APIError{Status: status, Code: code, Message: msg})
}
