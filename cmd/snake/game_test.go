package main

import (
	"bufio"
	"math/rand"
	"strings"
	"testing"
)

func TestStepMovesSnakeForward(t *testing.T) {
	g := testGame([]cell{{2, 2}, {1, 2}, {0, 2}}, dirRight, cell{5, 5}, 6, 6)

	g.step()

	assertState(t, g, statePlaying)
	assertSnake(t, g, []cell{{3, 2}, {2, 2}, {1, 2}})
	if g.score != 0 {
		t.Fatalf("score = %d, want 0", g.score)
	}
}

func TestEatingFoodGrowsAndScores(t *testing.T) {
	g := testGame([]cell{{2, 2}, {1, 2}, {0, 2}}, dirRight, cell{3, 2}, 6, 6)

	g.step()

	assertState(t, g, statePlaying)
	assertSnake(t, g, []cell{{3, 2}, {2, 2}, {1, 2}, {0, 2}})
	if g.score != 1 {
		t.Fatalf("score = %d, want 1", g.score)
	}
	if containsCell(g.snake, g.food) {
		t.Fatalf("food spawned on snake at %#v", g.food)
	}
}

func TestReverseTurnIsIgnored(t *testing.T) {
	g := testGame([]cell{{2, 2}, {1, 2}, {0, 2}}, dirRight, cell{5, 5}, 6, 6)

	g.turn(dirLeft)
	g.step()

	assertState(t, g, statePlaying)
	assertSnake(t, g, []cell{{3, 2}, {2, 2}, {1, 2}})
}

func TestWallCollisionEndsGame(t *testing.T) {
	g := testGame([]cell{{2, 1}, {1, 1}, {0, 1}}, dirRight, cell{0, 2}, 3, 3)

	g.step()

	assertState(t, g, stateLost)
	if g.lossReason != "wall" {
		t.Fatalf("lossReason = %q, want wall", g.lossReason)
	}
}

func TestSelfCollisionEndsGame(t *testing.T) {
	g := testGame([]cell{{2, 1}, {2, 2}, {1, 2}, {1, 1}}, dirDown, cell{0, 0}, 4, 4)

	g.step()

	assertState(t, g, stateLost)
	if g.lossReason != "self" {
		t.Fatalf("lossReason = %q, want self", g.lossReason)
	}
}

func TestMovingIntoVacatedTailIsAllowed(t *testing.T) {
	g := testGame([]cell{{1, 1}, {2, 1}, {2, 2}, {1, 2}}, dirDown, cell{0, 0}, 4, 4)

	g.step()

	assertState(t, g, statePlaying)
	assertSnake(t, g, []cell{{1, 2}, {1, 1}, {2, 1}, {2, 2}})
}

func TestFillingBoardWins(t *testing.T) {
	g := testGame([]cell{{1, 0}, {0, 0}}, dirRight, cell{2, 0}, 3, 1)

	g.step()

	assertState(t, g, stateWon)
	assertSnake(t, g, []cell{{2, 0}, {1, 0}, {0, 0}})
}

func TestNewGameRejectsTinyBoard(t *testing.T) {
	_, err := newGame(3, 3, 1)
	if err == nil {
		t.Fatal("newGame returned nil error for tiny board")
	}
}

func TestRenderShowsBoardPiecesAndStatus(t *testing.T) {
	g := testGame([]cell{{2, 1}, {1, 1}, {0, 1}}, dirRight, cell{3, 1}, 5, 4)

	board := g.render()

	for _, want := range []string{"Score: 0", "@", "o", "*"} {
		if !strings.Contains(board, want) {
			t.Fatalf("render() missing %q:\n%s", want, board)
		}
	}
}

func TestKeyEventParsesMovementAndQuit(t *testing.T) {
	tests := []struct {
		name string
		key  byte
		rest string
		want inputEvent
	}{
		{name: "w", key: 'w', want: inputEvent{kind: inputTurn, dir: dirUp}},
		{name: "s", key: 's', want: inputEvent{kind: inputTurn, dir: dirDown}},
		{name: "a", key: 'a', want: inputEvent{kind: inputTurn, dir: dirLeft}},
		{name: "d", key: 'd', want: inputEvent{kind: inputTurn, dir: dirRight}},
		{name: "q", key: 'q', want: inputEvent{kind: inputQuit}},
		{name: "ansi up", key: 27, rest: "[A", want: inputEvent{kind: inputTurn, dir: dirUp}},
		{name: "windows right", key: 224, rest: string([]byte{77}), want: inputEvent{kind: inputTurn, dir: dirRight}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := keyEvent(tt.key, bufio.NewReader(strings.NewReader(tt.rest)))
			if !ok {
				t.Fatal("keyEvent returned ok=false")
			}
			if got != tt.want {
				t.Fatalf("keyEvent() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func testGame(snake []cell, dir direction, food cell, width, height int) *game {
	return &game{
		width:   width,
		height:  height,
		snake:   append([]cell(nil), snake...),
		dir:     dir,
		pending: dir,
		food:    food,
		rng:     rand.New(rand.NewSource(1)),
		state:   statePlaying,
	}
}

func assertState(t *testing.T, g *game, want gameState) {
	t.Helper()

	if g.state != want {
		t.Fatalf("state = %v, want %v", g.state, want)
	}
}

func assertSnake(t *testing.T, g *game, want []cell) {
	t.Helper()

	if len(g.snake) != len(want) {
		t.Fatalf("snake length = %d, want %d; snake=%#v", len(g.snake), len(want), g.snake)
	}
	for i := range want {
		if g.snake[i] != want[i] {
			t.Fatalf("snake[%d] = %#v, want %#v; snake=%#v", i, g.snake[i], want[i], g.snake)
		}
	}
}

func containsCell(cells []cell, target cell) bool {
	for _, c := range cells {
		if c == target {
			return true
		}
	}
	return false
}
