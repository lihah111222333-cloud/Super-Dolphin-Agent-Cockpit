// Package provider defines provider-facing DTOs shared by module, adapter, and
// host-driver boundaries.
//
// The package owns wire-shaped request, result, manifest, and event payloads
// only. It must stay free of provider implementations, database records,
// logging, configuration loading, and runtime orchestration.
package provider
