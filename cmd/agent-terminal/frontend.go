package main

import (
	"embed"
	"io/fs"
)

// frontendDist embeds the Vite build output living next to the Vue source.
// Makefile build/test/run targets create dist before Go compiles this package.
// The all: prefix preserves nested assets and dot-files in the built output.
//
//go:embed all:frontend/dist
var frontendDist embed.FS

// Force Go compiler to re-embed fresh frontend dist assets (React 19 migration build - React 19 Dispatcher Fix, CWD Bootstrap Fix, Vue-compat Loop Detector Fix, and Memory Center Brutalist Design Spec Refactor - Infinite Loop Watcher Fix, React-Vue Reactivity Sync Fix, E2E Harness Test Fix, and SkillsPage React Child Fix).

func frontendDistFS() fs.FS {
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		return frontendDist
	}
	return sub
}
