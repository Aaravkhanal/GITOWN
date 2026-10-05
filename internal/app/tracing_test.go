package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestW3CTraceContextPropagation(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	trace := newTraceContext(parent)
	if trace.traceID != "4bf92f3577b34da6a3ce929d0e0e4736" || trace.flags != 1 || len(trace.spanID) != 16 || trace.spanID == "00f067aa0ba902b7" {
		t.Fatalf("trace context did not create a child span: %+v", trace)
	}
	if got := traceParent(trace); !strings.HasPrefix(got, "00-4bf92f3577b34da6a3ce929d0e0e4736-") {
		t.Fatalf("response traceparent lost the upstream trace: %q", got)
	}
	ctx := context.WithValue(context.Background(), traceContextKey{}, trace)
	if got, ok := traceFromContext(ctx); !ok || got != trace {
		t.Fatalf("trace context was not retrievable: %+v %v", got, ok)
	}
	invalid := newTraceContext("00-invalid")
	if len(invalid.traceID) != 32 || len(invalid.spanID) != 16 || invalid.traceID == strings.Repeat("0", 32) {
		t.Fatalf("invalid parent did not become a fresh trace: %+v", invalid)
	}
}

func TestHTTPHandlerReturnsCorrelatedTraceparent(t *testing.T) {
	app := &App{}
	request := httptest.NewRequest("GET", "/livez", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	parts := strings.Split(recorder.Header().Get("traceparent"), "-")
	if recorder.Code != 200 || len(parts) != 4 || parts[1] != "4bf92f3577b34da6a3ce929d0e0e4736" || parts[2] == "00f067aa0ba902b7" {
		t.Fatalf("handler did not return a child trace context: status=%d traceparent=%q", recorder.Code, recorder.Header().Get("traceparent"))
	}
}
