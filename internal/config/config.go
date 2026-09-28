package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Address       string
	DatabaseURL   string
	DataDir       string
	Origin        string
	GitURL        string
	SecureCookies bool
	Signup        bool
}

func Load() (Config, error) {
	c := Config{Address: value("GITOWN_ADDR", "127.0.0.1:8080"), DatabaseURL: os.Getenv("DATABASE_URL"), DataDir: value("GITOWN_DATA_DIR", ".data/repositories"), Origin: value("GITOWN_ORIGIN", "http://localhost:3000"), GitURL: value("GITOWN_GIT_URL", "http://localhost:8080/git"), Signup: os.Getenv("GITOWN_ALLOW_SIGNUP") == "true"}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
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
