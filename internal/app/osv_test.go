package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type osvRoundTripper func(*http.Request) (*http.Response, error)

func (f osvRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQueryOSVBatchAndDetails(t *testing.T) {
	client := &http.Client{Transport: osvRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/v1/querybatch":
			if r.Method != http.MethodPost {
				t.Fatalf("batch method = %s", r.Method)
			}
			var payload struct {
				Queries []osvQuery `json:"queries"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Queries) != 1 || payload.Queries[0].Package.Ecosystem != "npm" || payload.Queries[0].Package.Name != "example-pkg" || payload.Queries[0].Version != "1.0.0" {
				t.Fatalf("unexpected batch payload: %+v", payload)
			}
			body = `{"results":[{"vulns":[{"id":"GHSA-abcd-1234-efgh"}]}]}`
		case "/v1/vulns/GHSA-abcd-1234-efgh":
			body = `{"id":"GHSA-abcd-1234-efgh","summary":"Example vulnerability","severity":[{"type":"CVSS_V3","score":"8.2"}],"affected":[{"package":{"ecosystem":"npm","name":"example-pkg"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"1.0.1"}]}]}]}`
		default:
			return nil, io.EOF
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	items, err := queryOSV(context.Background(), client, "https://osv.test/v1", []osvEdge{{ecosystem: "npm", name: "example-pkg", version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "GHSA-abcd-1234-efgh" || items[0].Patched != "1.0.1" || items[0].Severity != "high" || items[0].Ecosystem != "npm" {
		t.Fatalf("unexpected OSV results: %+v", items)
	}
}

func TestWikiAttachmentPathAndTypeAllowlist(t *testing.T) {
	if _, ok := wikiAttachmentPath("guide", "../../evil.png"); ok {
		t.Fatal("accepted path traversal")
	}
	if _, ok := wikiAttachmentPath("guide", "image.svg"); !ok {
		t.Fatal("safe path syntax should pass; content validation is separate")
	}
	if wikiAttachmentTypes["image/svg+xml"] != "" || wikiAttachmentTypes["text/html"] != "" {
		t.Fatal("active content types must stay excluded")
	}
}

func TestOSVSkipsVersionRanges(t *testing.T) {
	for _, version := range []string{"^1.2.3", "~2.0", ">=1.0", "1.2.x", "*", "1.2.3 || 2.0.0"} {
		if isExactOSVVersion(version) {
			t.Fatalf("range treated as exact version: %q", version)
		}
	}
	for _, version := range []string{"1.2.3", "v1.2.3", "1.2.3.post1", "v0.0.0-20260102030405-abcd"} {
		if !isExactOSVVersion(version) {
			t.Fatalf("exact version rejected: %q", version)
		}
	}
}
