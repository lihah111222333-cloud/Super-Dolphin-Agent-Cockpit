// Package contract defines stable cross-layer interfaces, events, errors, and
// request/response contracts.
//
// Contracts may be consumed by modules, adapters, stores, and platform leaf
// packages, but this package must not depend on any of those implementations.
// Keep behavior out of this package except for small validation or sentinel
// helpers required to preserve boundary semantics.
package contract
