package mode

// Version is the build's version string, set at link time by the Makefile:
//
//	-ldflags "-X github.com/enowdev/succubus/internal/mode.Version=v1.2.3"
//
// It lives here rather than in package main so the MCP handshake and the CLI
// report the same thing — an MCP server announcing a different version from the
// binary serving it is a confusing thing to debug.
//
// The default deliberately does not name a release: a binary built from an
// arbitrary checkout should not claim to be a tagged version.
var Version = "dev"
