package main

import (
	"embed"
	"io/fs"
)

// frontendDist embeds the Vite build output living next to the Vue source.
// The all: prefix ensures dot-files like .gitkeep are included so the
// embed pattern always matches — even before `npm run build` has been run.
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
