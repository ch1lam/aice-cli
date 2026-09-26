package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ControlMode is frozen by the application when it creates a run binding.
type ControlMode string

const (
	BackgroundOnly    ControlMode = "background_only"
	ForegroundAllowed ControlMode = "foreground_allowed"
)

type RunOptions struct {
	Mode   ControlMode
	Images bool
}

// Status is a content-free, cached projection. Reading it never captures a
// window, launches a process, or asks for OS authorization.
type Status struct {
	Connected  bool
	Generation uint64
	Diagnostic string
}

type driverClient interface {
	call(context.Context, string, any) (Reply, error)
	close() error
}

// Manager owns one connection and serializes every observation/action sequence.
// Config publication, installation and OS authorization belong to the app.
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	gate   chan struct{}
	dial   func(context.Context) (driverClient, error)
	client driverClient // all connection/run/reference fields are gate-owned
	runs   map[*Run]struct{}
	latest map[windowIdentity]string

	mu     sync.Mutex // status only; never held across I/O
	status Status
}

// newManager stays private until the native runtime's verified identity and
// standard-mode preflight are wired. Merely knowing a binary path is not proof
// that an existing shared service uses the requested permission mode.
func newManager(dial func(context.Context) (driverClient, error)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1), dial: dial, runs: make(map[*Run]struct{}), latest: make(map[windowIdentity]string)}
	m.gate <- struct{}{}
	return m
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Bind performs no native I/O. Each binding owns its cancellation, observations
// and public Cua session label, even when two application runs share a Driver.
func (m *Manager) Bind(ctx context.Context, options RunOptions) (*Run, error) {
	if options.Mode != BackgroundOnly && options.Mode != ForegroundAllowed {
		return nil, errors.New("desktop: invalid frozen control mode")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.ctx.Err(); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	r := &Run{manager: m, ctx: runCtx, cancel: cancel, options: options, id: "aice-" + rand.Text(), targets: make(map[string]windowIdentity), observations: make(map[string]observationBinding)}
	r.stopManager = context.AfterFunc(m.ctx, cancel)
	return r, nil
}

type Run struct {
	manager      *Manager
	ctx          context.Context
	cancel       context.CancelFunc
	stopManager  func() bool
	closed       atomic.Bool
	id           string
	options      RunOptions
	started      bool // gate-owned
	active       bool
	targets      map[string]windowIdentity
	observations map[string]observationBinding
}

// acquire joins call and run cancellation. Cancellation invalidates a run even
// while a dispatched action is still settling; the next caller cannot slip in.
func (r *Run) acquire(ctx context.Context) (context.Context, func(), error) {
	joined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	releaseContext := func() { stop(); cancel() }
	if r.closed.Load() || r.ctx.Err() != nil || r.manager.ctx.Err() != nil {
		releaseContext()
		return nil, nil, context.Canceled
	}
	select {
	case <-joined.Done():
		releaseContext()
		return nil, nil, joined.Err()
	case <-r.manager.gate:
	}
	if r.closed.Load() || r.ctx.Err() != nil || joined.Err() != nil {
		r.manager.gate <- struct{}{}
		releaseContext()
		return nil, nil, context.Canceled
	}
	return joined, func() {
		if r.manager.ctx.Err() != nil {
			_ = r.manager.disconnectLocked("Manager closed")
		}
		r.manager.gate <- struct{}{}
		releaseContext()
	}, nil
}

func (r *Run) ensureLocked(ctx context.Context) error {
	m := r.manager
	if m.client == nil {
		c, err := m.dial(ctx)
		if err != nil {
			m.setDiagnostic("Driver connection unavailable")
			return err
		}
		m.client = c
		m.mu.Lock()
		m.status.Generation++
		m.status.Connected = true
		m.status.Diagnostic = ""
		m.mu.Unlock()
	}
	if r.started {
		if r.active {
			return nil
		}
		return errors.New("desktop: Driver session unavailable; start a new run")
	}
	// Track the session before dispatch so Close attempts bounded cleanup even
	// if start_session executed but its response was lost.
	m.runs[r] = struct{}{}
	r.started = true
	reply, err := r.callLocked(ctx, "start_session", map[string]any{"session": r.id})
	if err != nil {
		return err
	}
	var state struct {
		Active bool `json:"active"`
	}
	if reply.IsError || json.Unmarshal(reply.Structured, &state) != nil || !state.Active {
		return errors.New("desktop: Driver session unavailable")
	}
	r.active = true
	return nil
}

// callLocked never retries. A transport failure retires the connection; a later
// read may reconnect, but cannot recover execution references from that epoch.
func (r *Run) callLocked(ctx context.Context, name string, args any) (Reply, error) {
	reply, err := r.manager.client.call(ctx, name, args)
	var before beforeDispatchError
	if err != nil && !errors.As(err, &before) {
		_ = r.manager.disconnectLocked("Driver connection lost; dispatched outcome may be unknown")
	}
	return reply, err
}

func (m *Manager) setDiagnostic(text string) {
	m.mu.Lock()
	m.status.Diagnostic = text
	m.mu.Unlock()
}

func (m *Manager) disconnectLocked(reason string) error {
	var err error
	if m.client != nil {
		err = m.client.close()
		m.client = nil
	}
	for run := range m.runs {
		run.started = false
		run.active = false
		clear(run.targets)
		clear(run.observations)
	}
	clear(m.runs)
	clear(m.latest)
	m.mu.Lock()
	m.status.Connected = false
	m.status.Diagnostic = reason
	m.mu.Unlock()
	return err
}

const cleanupTimeout = 3 * time.Second

// Close first invalidates the binding, then ends only its own Cua session.
// It neither closes user applications nor sends a global Driver stop/revoke.
func (r *Run) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.cancel()
	r.stopManager()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return fmt.Errorf("desktop: session cleanup: %w", ctx.Err())
	case <-r.manager.gate:
	}
	defer func() { r.manager.gate <- struct{}{} }()
	defer delete(r.manager.runs, r)
	clear(r.targets)
	r.clearObservationsLocked()
	if !r.started || r.manager.client == nil {
		return nil
	}
	r.started = false
	r.active = false
	reply, err := r.callLocked(ctx, "end_session", map[string]any{"session": r.id})
	if err == nil && reply.IsError {
		err = errors.New("desktop: Driver session cleanup incomplete")
	}
	return err
}

func (m *Manager) Close() error {
	m.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return fmt.Errorf("desktop: connection cleanup: %w", ctx.Err())
	case <-m.gate:
	}
	defer func() { m.gate <- struct{}{} }()
	return m.disconnectLocked("Manager closed")
}
