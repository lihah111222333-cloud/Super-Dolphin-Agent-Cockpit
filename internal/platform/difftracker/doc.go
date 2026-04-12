// Package difftracker provides git-backed snapshot and diff helpers for
// tool-driven workspace changes.
//
// The package captures a repository snapshot before a tracked tool mutation
// and later renders unified diffs from the updated working tree.
//
// It now only keeps the core git snapshot/diff primitives plus the small
// support types used by toolbridge to resolve agent working directories and
// emit diff payloads.
package difftracker
