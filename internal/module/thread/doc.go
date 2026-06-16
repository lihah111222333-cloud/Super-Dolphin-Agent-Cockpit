// Package thread owns agent thread lifecycle, routing, history, and launch use
// cases.
//
// It must not own provider transport internals, UI rendering, or raw database
// driver access.
package thread
