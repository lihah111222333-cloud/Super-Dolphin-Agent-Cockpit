// Package promptrouting owns pure prompt-template routing rules for thread
// start and spawn flows.
//
// It must not own thread lifecycle orchestration, provider process launch,
// persistence, logging, or UI state. Callers pass already-loaded prompt
// templates and receive deterministic selections or assembler-ready blocks.
package promptrouting
