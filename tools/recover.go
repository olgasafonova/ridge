package tools

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"time"
)

// recoverPanic recovers from panics in tool handlers and converts them into a
// structured error with a correlation ID. The panic value and stack are logged
// server-side; only the correlation ID reaches the MCP caller.
//
// MUST be called as `defer h.recoverPanic(spec.Name, &retErr)` from a function
// with NAMED return values. Without named returns the deferred reassignment
// is a no-op and panics surface as silent fake-success responses.
func (h *HandlerRegistry) recoverPanic(toolName string, errPtr *error) {
	r := recover()
	if r == nil {
		return
	}
	corrID := newCorrelationID()
	h.logger.Error("Panic recovered",
		"tool", toolName,
		"correlation_id", corrID,
		"panic", r,
		"stack", string(debug.Stack()))
	if errPtr != nil {
		*errPtr = fmt.Errorf("%s: internal error (correlation_id=%s)", toolName, corrID)
	}
}

// newCorrelationID returns a short hex string for log correlation. Falls back
// to a timestamp-based ID if crypto/rand is unavailable (vanishingly rare).
func newCorrelationID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
