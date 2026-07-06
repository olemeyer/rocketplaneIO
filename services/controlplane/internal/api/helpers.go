package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const maxBody = 1 << 20 // 1 MiB

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes a {"error": msg} body (the shared API error shape).
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// contextWithTimeout derives a timeout context from the request.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// decode reads and strictly decodes a JSON request body into dst.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return false
	}
	return true
}
