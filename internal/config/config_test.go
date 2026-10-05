package config

import (
	"net"
	"testing"
)

func TestLoadTrustedProxies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/gitown")
	t.Setenv("GITOWN_ORIGIN", "http://localhost:3000")
	t.Setenv("GITOWN_GIT_URL", "http://localhost:8080/git")
	t.Setenv("GITOWN_DATA_DIR", t.TempDir())
	t.Setenv("GITOWN_TRUSTED_PROXIES", "10.0.0.2, 192.0.2.0/24")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 2 || !cfg.TrustedProxies[0].Contains(net.ParseIP("10.0.0.2")) || !cfg.TrustedProxies[1].Contains(net.ParseIP("192.0.2.15")) {
		t.Fatalf("trusted proxy list was not parsed correctly: %+v", cfg.TrustedProxies)
	}
	t.Setenv("GITOWN_TRUSTED_PROXIES", "not-a-network")
	if _, err = Load(); err == nil {
		t.Fatal("invalid trusted proxy configuration was accepted")
	}
}
