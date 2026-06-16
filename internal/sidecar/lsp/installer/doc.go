// Package installer owns LSP sidecar binary discovery and best-effort
// auto-installation.
//
// The package is an LSP sidecar library. It may call process tools needed to
// locate language servers, but it must not own LSP request routing, editor
// protocol mapping, or workspace lifecycle decisions.
package installer
