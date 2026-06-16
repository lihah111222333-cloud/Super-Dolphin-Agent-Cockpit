// Package dedup owns memory-entry duplicate detection and merge decisions.
//
// It is computational and must not write files, call stores directly, or depend
// on transport adapters.
package dedup
