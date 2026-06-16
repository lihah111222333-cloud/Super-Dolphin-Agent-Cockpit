// Package kernel owns low-dependency platform primitives shared by modules,
// adapters, and platform packages.
//
// It may contain pure normalization, ID/path helpers, retry helpers, and
// compatibility wrappers. It must not own business workflows, provider protocol
// handling, persistence, UI state, or process startup.
package kernel
