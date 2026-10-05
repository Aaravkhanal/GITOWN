package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

type traceContext struct {
	traceID string
	spanID  string
	flags   byte
}

type traceContextKey struct{}

func newTraceContext(parent string) traceContext {
	trace := traceContext{flags: 1}
	parts := strings.Split(parent, "-")
	if len(parts) == 4 && parts[0] == "00" && len(parts[1]) == 32 && len(parts[2]) == 16 && len(parts[3]) == 2 && parts[1] == strings.ToLower(parts[1]) && parts[2] == strings.ToLower(parts[2]) && parts[3] == strings.ToLower(parts[3]) && parts[1] != strings.Repeat("0", 32) && parts[2] != strings.Repeat("0", 16) {
		if decoded, err := hex.DecodeString(parts[1] + parts[2] + parts[3]); err == nil {
			return traceContext{traceID: parts[1], spanID: randomHex(8), flags: decoded[24]}
		}
	}
	trace.traceID = randomHex(16)
	trace.spanID = randomHex(8)
	return trace
}

func randomHex(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return strings.Repeat("0", size*2)
	}
	return hex.EncodeToString(data)
}

func traceFromContext(ctx context.Context) (traceContext, bool) {
	trace, ok := ctx.Value(traceContextKey{}).(traceContext)
	return trace, ok
}

func traceParent(trace traceContext) string {
	return "00-" + trace.traceID + "-" + trace.spanID + "-" + hex.EncodeToString([]byte{trace.flags})
}
