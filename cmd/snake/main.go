package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

const (
	defaultWidth  = 24
	defaultHeight = 16
	defaultSpeed  = 120 * time.Millisecond
)

type inputKind int

const (
	inputTurn inputKind = iota
	inputQuit
)

type inputEvent struct {
	kind inputKind
	dir  direction
}

func main() {
	width := flag.Int("width", defaultWidth, "board width")
	height := flag.Int("height", defaultHeight, "board height")
	speed := flag.Duration("speed", defaultSpeed, "time between moves")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed")
	flag.Parse()

	if err := run(os.Stdin, os.Stdout, *width, *height, *speed, *seed); err != nil {
		slog.Error("snake failed", "error", err)
		os.Exit(1)
	}
}

func run(in *os.File, out io.Writer, width, height int, speed time.Duration, seed int64) error {
	g, err := newGame(width, height, seed)
	if err != nil {
		return err
	}
	if speed <= 0 {
		return fmt.Errorf("speed must be positive")
	}

	restore, err := setupTerminal(in, out)
	if err != nil {
		return err
	}
	defer restore()

	inputs := make(chan inputEvent, 8)
	go readInput(in, inputs)

	ticker := time.NewTicker(speed)
	defer ticker.Stop()

	draw(out, g)
	for g.state == statePlaying {
		select {
		case ev := <-inputs:
			if ev.kind == inputQuit {
				return nil
			}
			g.turn(ev.dir)
		case <-ticker.C:
			g.step()
			draw(out, g)
		}
	}

	draw(out, g)
	return nil
}

func readInput(in io.Reader, out chan<- inputEvent) {
	reader := bufio.NewReader(in)
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return
		}

		if ev, ok := keyEvent(b, reader); ok {
			out <- ev
		}
	}
}

func keyEvent(b byte, reader *bufio.Reader) (inputEvent, bool) {
	switch b {
	case 'q', 'Q':
		return inputEvent{kind: inputQuit}, true
	case 'w', 'W':
		return inputEvent{kind: inputTurn, dir: dirUp}, true
	case 's', 'S':
		return inputEvent{kind: inputTurn, dir: dirDown}, true
	case 'a', 'A':
		return inputEvent{kind: inputTurn, dir: dirLeft}, true
	case 'd', 'D':
		return inputEvent{kind: inputTurn, dir: dirRight}, true
	case 0, 224:
		return windowsArrowKey(reader)
	case 27:
		return ansiArrowKey(reader)
	default:
		return inputEvent{}, false
	}
}

func ansiArrowKey(reader *bufio.Reader) (inputEvent, bool) {
	next, err := reader.ReadByte()
	if err != nil || next != '[' {
		return inputEvent{}, false
	}

	code, err := reader.ReadByte()
	if err != nil {
		return inputEvent{}, false
	}

	switch code {
	case 'A':
		return inputEvent{kind: inputTurn, dir: dirUp}, true
	case 'B':
		return inputEvent{kind: inputTurn, dir: dirDown}, true
	case 'C':
		return inputEvent{kind: inputTurn, dir: dirRight}, true
	case 'D':
		return inputEvent{kind: inputTurn, dir: dirLeft}, true
	default:
		return inputEvent{}, false
	}
}

func windowsArrowKey(reader *bufio.Reader) (inputEvent, bool) {
	code, err := reader.ReadByte()
	if err != nil {
		return inputEvent{}, false
	}

	switch code {
	case 72:
		return inputEvent{kind: inputTurn, dir: dirUp}, true
	case 80:
		return inputEvent{kind: inputTurn, dir: dirDown}, true
	case 77:
		return inputEvent{kind: inputTurn, dir: dirRight}, true
	case 75:
		return inputEvent{kind: inputTurn, dir: dirLeft}, true
	default:
		return inputEvent{}, false
	}
}

func draw(out io.Writer, g *game) {
	fmt.Fprint(out, "\x1b[?25l\x1b[H\x1b[2J")
	fmt.Fprint(out, g.render())
}
