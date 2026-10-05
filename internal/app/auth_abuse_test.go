package app

import (
	"net"
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustsOnlyConfiguredProxyChain(t *testing.T) {
	_, trustedProxy, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		remote string
		xff    string
		trust  []net.IPNet
		want   string
	}{
		{name: "ignore untrusted forwarded header", remote: "203.0.113.7:443", xff: "198.51.100.9", trust: []net.IPNet{*trustedProxy}, want: "203.0.113.7"},
		{name: "walk trusted proxy chain", remote: "10.0.0.2:443", xff: "198.51.100.9, 10.0.0.1", trust: []net.IPNet{*trustedProxy}, want: "198.51.100.9"},
		{name: "invalid chain falls back to peer", remote: "10.0.0.2:443", xff: "not-an-ip", trust: []net.IPNet{*trustedProxy}, want: "10.0.0.2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = test.remote
			req.Header.Set("X-Forwarded-For", test.xff)
			if got := trustedClientIP(req, test.trust); got != test.want {
				t.Fatalf("trustedClientIP() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAbuseDigestUsesConfiguredSecret(t *testing.T) {
	t.Setenv("GITOWN_SECRET_KEY", "stable-test-key")
	first := abuseDigest("ip:192.0.2.1")
	if first == "" || first == "192.0.2.1" {
		t.Fatal("abuse fingerprint must not expose its input")
	}
	t.Setenv("GITOWN_SECRET_KEY", "rotated-test-key")
	if second := abuseDigest("ip:192.0.2.1"); second == first {
		t.Fatal("abuse fingerprint should be keyed by the stable deployment secret")
	}
}
