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

func frontendDistFS() fs.FS {
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		return frontendDist
	}
	return sub
}
