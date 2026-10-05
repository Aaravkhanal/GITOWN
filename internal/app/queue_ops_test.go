package app

import (
	"strings"
	"testing"
)

func TestSafeQueueErrorRedactsAddressesAndURLs(t *testing.T) {
	got := safeQueueError("delivery to person@example.test failed at https://hooks.example.test/path?token=private", 500)
	if strings.Contains(got, "person@example.test") || strings.Contains(got, "https://") || strings.Contains(got, "token=private") {
		t.Fatalf("queue error leaked a recipient or URL: %q", got)
	}
	if !strings.Contains(got, "[address]") || !strings.Contains(got, "[url]") {
		t.Fatalf("queue error lost useful diagnostic context: %q", got)
	}
}
