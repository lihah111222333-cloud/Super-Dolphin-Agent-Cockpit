// Package turn owns turn submission, tracking, dedupe, and trajectory use cases.
//
// It must not own provider transport internals, UI rendering, or raw database
// driver access.
package turn
