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
	Connected        bool
	Generation       uint64
	Diagnostic       string
	CaptureCheckedAt time.Time
	CaptureAvailable bool
}

type driverClient interface {
	call(context.Context, string, any) (Reply, error)
	close() error
}

// Manager owns one connection and serializes every observation/action sequence.
// Config publication, installation and OS authorization belong to the app.
type Manager struct {
	platform  string // immutable native wire contract; never selected by the model
	ctx       context.Context
	cancel    context.CancelFunc
	gate      chan struct{}
	dial      func(context.Context) (driverClient, error)
	client    driverClient // all connection/run/reference fields are gate-owned
	runs      map[*Run]struct{}
	latest    map[windowIdentity]string
	occupy    func(context.Context) (func() error, error)
	unlock    func() error
	occupants map[*Run]struct{} // survives connection loss until run cleanup

	mu     sync.Mutex // status only; never held across I/O
	status Status
}

// newManager is the transport seam used by offline tests. Public construction
// requires the native runtime's verified identity and standard-mode preflight.
func newManager(dial func(context.Context) (driverClient, error)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{platform: "darwin", ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1), dial: dial, runs: make(map[*Run]struct{}), latest: make(map[windowIdentity]string), occupants: make(map[*Run]struct{})}
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
	r := &Run{manager: m, ctx: runCtx, cancel: cancel, options: options, id: "aice-" + rand.Text(), targets: make(map[string]windowIdentity), apps: make(map[string]appLaunchTarget), observations: make(map[string]observationBinding)}
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
	cleanupDone  bool
	cleanupErr   error
	targets      map[string]windowIdentity
	apps         map[string]appLaunchTarget // opaque reference -> native discovered launcher
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
		} else if r.closed.Load() {
			// Close may have exhausted its wait while this call was settling.
			// The execution owner completes cleanup before releasing the gate.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
			if err := r.closeLocked(cleanupCtx); err != nil {
				r.manager.setDiagnostic("Run ended with incomplete native cleanup")
			}
			cleanupCancel()
		}
		r.manager.gate <- struct{}{}
		releaseContext()
	}, nil
}

func (r *Run) ensureLocked(ctx context.Context) error {
	m := r.manager
	if _, exists := m.occupants[r]; !exists {
		if m.unlock == nil && m.occupy != nil {
			unlock, err := m.occupy(ctx)
			if err != nil {
				m.setDiagnostic("Desktop is occupied or its coordination lock is unavailable")
				return err
			}
			m.unlock = unlock
		}
		m.occupants[r] = struct{}{}
	}
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

func (m *Manager) recordCapture(available bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.CaptureCheckedAt, m.status.CaptureAvailable = time.Now(), available
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
		clear(run.apps)
		clear(run.observations)
	}
	clear(m.runs)
	clear(m.latest)
	m.mu.Lock()
	m.status.Connected = false
	m.status.Diagnostic = reason
	m.mu.Unlock()
	if m.ctx.Err() != nil {
		clear(m.occupants)
		err = errors.Join(err, m.releaseOccupancyLocked())
	}
	return err
}

func (m *Manager) releaseOccupancyLocked() error {
	if len(m.occupants) != 0 || m.unlock == nil {
		return nil
	}
	unlock := m.unlock
	m.unlock = nil
	return unlock()
}

const cleanupTimeout = 3 * time.Second

// Disconnect retires only this manager's connection and execution references.
// The application uses it under its idle setup reservation before OS grants or
// repair, so the next run performs fresh service admission. No daemon is stopped.
func (m *Manager) Disconnect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.gate:
	}
	defer func() { m.gate <- struct{}{} }()
	return m.disconnectLocked("Connection retired for setup; next run will verify readiness")
}

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
	return r.closeLocked(ctx)
}

func (r *Run) closeLocked(ctx context.Context) (returnErr error) {
	if r.cleanupDone {
		return r.cleanupErr
	}
	defer func() {
		delete(r.manager.runs, r)
		delete(r.manager.occupants, r)
		returnErr = errors.Join(returnErr, r.manager.releaseOccupancyLocked())
		r.cleanupDone, r.cleanupErr = true, returnErr
	}()
	clear(r.targets)
	clear(r.apps)
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
