package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
)

const minBoardSize = 5

type cell struct {
	x int
	y int
}

type direction struct {
	dx int
	dy int
}

var (
	dirUp    = direction{dy: -1}
	dirDown  = direction{dy: 1}
	dirLeft  = direction{dx: -1}
	dirRight = direction{dx: 1}
)

type gameState int

const (
	statePlaying gameState = iota
	stateWon
	stateLost
)

type game struct {
	width      int
	height     int
	snake      []cell
	dir        direction
	pending    direction
	food       cell
	rng        *rand.Rand
	state      gameState
	score      int
	lossReason string
}

func newGame(width, height int, seed int64) (*game, error) {
	if width < minBoardSize || height < minBoardSize {
		return nil, fmt.Errorf("board must be at least %dx%d", minBoardSize, minBoardSize)
	}

	center := cell{x: width / 2, y: height / 2}
	g := &game{
		width:   width,
		height:  height,
		snake:   []cell{center, {x: center.x - 1, y: center.y}, {x: center.x - 2, y: center.y}},
		dir:     dirRight,
		pending: dirRight,
		rng:     rand.New(rand.NewSource(seed)),
		state:   statePlaying,
	}
	if !g.spawnFood() {
		return nil, errors.New("board has no room for food")
	}
	return g, nil
}

func (g *game) turn(next direction) {
	if g.state != statePlaying || next == (direction{}) || next.opposite(g.dir) {
		return
	}
	g.pending = next
}

func (g *game) step() {
	if g.state != statePlaying {
		return
	}

	g.dir = g.pending
	head := g.snake[0].move(g.dir)
	if !g.inBounds(head) {
		g.lose("wall")
		return
	}

	grow := head == g.food
	bodyLimit := len(g.snake)
	if !grow {
		bodyLimit--
	}
	if g.hitsBody(head, bodyLimit) {
		g.lose("self")
		return
	}

	nextSnake := make([]cell, 0, len(g.snake)+1)
	nextSnake = append(nextSnake, head)
	nextSnake = append(nextSnake, g.snake...)
	if grow {
		g.score++
	} else {
		nextSnake = nextSnake[:len(g.snake)]
	}
	g.snake = nextSnake

	if len(g.snake) == g.width*g.height {
		g.state = stateWon
		return
	}
	if grow && !g.spawnFood() {
		g.state = stateWon
	}
}

func (g *game) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Score: %d\n", g.score)
	b.WriteString("WASD or arrow keys to move, q to quit\n")

	borderWidth := g.width + 2
	b.WriteString(strings.Repeat("#", borderWidth))
	b.WriteByte('\n')
	for y := 0; y < g.height; y++ {
		b.WriteByte('#')
		for x := 0; x < g.width; x++ {
			b.WriteByte(g.runeAt(cell{x: x, y: y}))
		}
		b.WriteByte('#')
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat("#", borderWidth))
	b.WriteByte('\n')

	switch g.state {
	case stateWon:
		b.WriteString("You win. The board is full.\n")
	case stateLost:
		fmt.Fprintf(&b, "Game over: hit %s.\n", g.lossReason)
	}
	return b.String()
}

func (g *game) spawnFood() bool {
	free := make([]cell, 0, g.width*g.height-len(g.snake))
	for y := 0; y < g.height; y++ {
		for x := 0; x < g.width; x++ {
			c := cell{x: x, y: y}
			if !g.occupies(c) {
				free = append(free, c)
			}
		}
	}
	if len(free) == 0 {
		return false
	}

	g.food = free[g.rng.Intn(len(free))]
	return true
}

func (g *game) runeAt(c cell) byte {
	if g.snake[0] == c {
		return '@'
	}
	for _, body := range g.snake[1:] {
		if body == c {
			return 'o'
		}
	}
	if g.food == c && g.state == statePlaying {
		return '*'
	}
	return ' '
}

func (g *game) occupies(c cell) bool {
	for _, part := range g.snake {
		if part == c {
			return true
		}
	}
	return false
}

func (g *game) hitsBody(c cell, limit int) bool {
	for _, part := range g.snake[:limit] {
		if part == c {
			return true
		}
	}
	return false
}

func (g *game) inBounds(c cell) bool {
	return c.x >= 0 && c.x < g.width && c.y >= 0 && c.y < g.height
}

func (g *game) lose(reason string) {
	g.state = stateLost
	g.lossReason = reason
}

func (c cell) move(dir direction) cell {
	return cell{x: c.x + dir.dx, y: c.y + dir.dy}
}

func (dir direction) opposite(other direction) bool {
	return dir.dx+other.dx == 0 && dir.dy+other.dy == 0
}
