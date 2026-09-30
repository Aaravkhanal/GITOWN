// Package version holds the build version shared by the server and CLI.
package version

// Version is replaced at build time with
// -ldflags "-X github.com/Aaravkhanal/GITOWN/internal/version.Version=0.1.0".
// The value comes from the root package.json so the web app, API, and CLI
// report the same release. Source builds without the flag report "dev".
var Version = "dev"
