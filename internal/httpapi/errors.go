package httpapi

import (
	"encoding/json"
	"net/http"
)

// writeError writes a fixed JSON error body. It never echoes any request
// header or value. Issue #8 owns the final error shape; keep it in one place.
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
