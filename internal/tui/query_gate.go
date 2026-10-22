package tui

import (
	"context"
	"sync"
)

// queryGate owns frontend query commands. Bubble Tea can still run queued
// commands after it exits, so the gate refuses work once shutdown begins and
// waits for commands that already started.
type queryGate struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

// begin admits one command. Every admitted command must call done.
func (g *queryGate) begin() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

func (g *queryGate) done() { g.wg.Done() }

// close refuses new commands, cancels the started ones and waits for them.
func (g *queryGate) close(cancel context.CancelFunc) {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	cancel()
	g.wg.Wait()
}
