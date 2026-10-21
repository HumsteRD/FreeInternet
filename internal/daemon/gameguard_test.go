package daemon

import (
	"testing"
	"time"
)

func TestGameWatch(t *testing.T) {
	start := time.Now()
	g := gameWatch{at: start}
	if g.due(start.Add(time.Second)) {
		t.Fatal("сразу после начала игры проверять рано: игра ещё грузится")
	}
	if !g.due(start.Add(gameCheckEvery)) || g.due(start.Add(gameCheckEvery+time.Second)) {
		t.Fatal("проверка — раз в gameCheckEvery")
	}
	if g.note(false) {
		t.Fatal("одна неудача — ещё не повод выключать обход: сеть могла мигнуть")
	}
	if g.note(true) || g.note(false) {
		t.Fatal("удачная проверка обнуляет счёт")
	}
	if !g.note(false) {
		t.Fatalf("после %d неудач подряд обход нужно выключить", gameFailsToStop)
	}
}
