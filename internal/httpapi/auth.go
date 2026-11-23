package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// tokenBytes is the entropy of the per-start token: 256 bits, above the
// 128 bit minimum.
const tokenBytes = 32

// NewToken returns a fresh random token (base64url, no padding). Callers must
// treat an error as fatal rather than fall back to a weaker token.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// requireToken is the inner layer, applied to every /api route (including
// unknown paths, so an unauthenticated caller cannot map routes). The bearer
// Authorization header is the only accepted channel: there is deliberately no
// query-string fallback, since URLs land in history, proxies and logs.
func requireToken(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r, want) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validBearer requires exactly one Authorization header with the Bearer
// scheme (case-insensitive). Both sides are hashed so the constant-time
// compare sees equal-length inputs and does not leak the token length.
func validBearer(r *http.Request, want [sha256.Size]byte) bool {
	vals := r.Header["Authorization"]
	if len(vals) != 1 {
		return false
	}
	scheme, presented, ok := strings.Cut(vals[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
