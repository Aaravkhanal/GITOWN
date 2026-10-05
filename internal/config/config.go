package config

import (
	"errors"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Address       string
	DatabaseURL   string
	DataDir       string
	Origin        string
	GitURL        string
	SecureCookies bool
	Signup        bool
	// SMTP delivery is optional. Without an address, GITOWN keeps an honest
	// delivery record and marks outgoing mail as suppressed.
	SMTPAddr       string
	SMTPFrom       string
	SMTPUser       string
	SMTPPassword   string
	DigestInterval time.Duration
	// OSV lookups send dependency names and versions to api.osv.dev. Keep this
	// opt-in so private repository metadata is never shared by surprise.
	OSVEnabled     bool
	TrustedProxies []net.IPNet
}

func Load() (Config, error) {
	c := Config{Address: value("GITOWN_ADDR", "127.0.0.1:8080"), DatabaseURL: os.Getenv("DATABASE_URL"), DataDir: value("GITOWN_DATA_DIR", ".data/repositories"), Origin: value("GITOWN_ORIGIN", "http://localhost:3000"), GitURL: value("GITOWN_GIT_URL", "http://localhost:8080/git"), Signup: os.Getenv("GITOWN_ALLOW_SIGNUP") == "true"}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	c.SMTPAddr = strings.TrimSpace(os.Getenv("GITOWN_SMTP_ADDR"))
	c.OSVEnabled = os.Getenv("GITOWN_OSV_ENABLED") == "true"
	c.SMTPFrom = strings.TrimSpace(os.Getenv("GITOWN_SMTP_FROM"))
	c.SMTPUser = os.Getenv("GITOWN_SMTP_USER")
	c.SMTPPassword = os.Getenv("GITOWN_SMTP_PASSWORD")
	for _, raw := range strings.Split(os.Getenv("GITOWN_TRUSTED_PROXIES"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if ip := net.ParseIP(raw); ip != nil {
			maskBits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				maskBits = 32
			}
			c.TrustedProxies = append(c.TrustedProxies, net.IPNet{IP: ip, Mask: net.CIDRMask(maskBits, maskBits)})
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return c, errors.New("GITOWN_TRUSTED_PROXIES must be a comma-separated list of IP addresses or CIDRs")
		}
		c.TrustedProxies = append(c.TrustedProxies, *network)
	}
	if c.SMTPAddr != "" {
		if _, _, err := net.SplitHostPort(c.SMTPAddr); err != nil {
			return c, errors.New("GITOWN_SMTP_ADDR must be host:port")
		}
		if _, err := mail.ParseAddress(c.SMTPFrom); err != nil || strings.ContainsAny(c.SMTPFrom, "\r\n") {
			return c, errors.New("GITOWN_SMTP_FROM must be a valid sender address when GITOWN_SMTP_ADDR is set")
		}
	} else if c.SMTPUser != "" || c.SMTPFrom != "" {
		return c, errors.New("GITOWN_SMTP_ADDR is required when other SMTP settings are set")
	}
	c.DigestInterval = time.Hour
	if raw := os.Getenv("GITOWN_DIGEST_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval < time.Minute || interval > 7*24*time.Hour {
			return c, errors.New("GITOWN_DIGEST_INTERVAL must be a duration between 1m and 168h")
		}
		c.DigestInterval = interval
	}
	for _, raw := range []string{c.Origin, c.GitURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("GITOWN_ORIGIN and GITOWN_GIT_URL must be absolute HTTP(S) URLs")
		}
	}
	origin, _ := url.Parse(c.Origin)
	if origin.Path != "" && origin.Path != "/" {
		return c, errors.New("GITOWN_ORIGIN must not have a path")
	}
	c.Origin = strings.TrimRight(c.Origin, "/")
	c.GitURL = strings.TrimRight(c.GitURL, "/")
	c.SecureCookies = origin.Scheme == "https"
	if !c.SecureCookies && origin.Hostname() != "localhost" && origin.Hostname() != "127.0.0.1" {
		return c, errors.New("non-local installations require HTTPS")
	}
	var err error
	c.DataDir, err = filepath.Abs(c.DataDir)
	return c, err
}

func value(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
