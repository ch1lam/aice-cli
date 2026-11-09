package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// mcpOwnedConnection is the application lifecycle boundary. The client owns
// protocol state and any child process; an owner never stops shared services.
type mcpOwnedConnection interface {
	mcpCatalogConnection
	Close() error
}
type mcpOpenFunc func(context.Context, mcpclient.Config) (mcpOwnedConnection, error)

func openMCPConnection(ctx context.Context, configuration mcpclient.Config) (mcpOwnedConnection, error) {
	client, err := mcpclient.Open(ctx, configuration)
	if client == nil {
		return nil, err
	}
	return client, err
}

// mcpState is a service status shown to users and models. Connection states
// are observed and stored; disabled, needs_approval and policy needs_auth are
// derived on read by blockLocked, so a policy that passes again stops showing.
type mcpState string

const (
	mcpDisconnected  mcpState = "disconnected"
	mcpConnecting    mcpState = "connecting"
	mcpReady         mcpState = "ready"
	mcpFailed        mcpState = "failed"
	mcpNeedsAuth     mcpState = "needs_auth"
	mcpNeedsApproval mcpState = "needs_approval"
	mcpDisabled      mcpState = "disabled"
)

type mcpServiceStatus struct {
	Key           string
	State         mcpState
	Detail        string
	ToolCount     int
	CatalogKnown  bool
	EligibleTools int
}

type mcpOwnedService struct {
	server                                       config.MCPServer
	gate                                         chan struct{}
	ctx                                          context.Context
	cancel                                       context.CancelFunc
	client                                       mcpOwnedConnection
	generation, clientGeneration                 uint64
	resourceGeneration, clientResourceGeneration uint64
	revoked                                      bool
	authFailed                                   bool
	secrets                                      []string
	// status holds the last observed connection outcome; see observe.
	status mcpServiceStatus
}

// observe records a connection outcome. It is the only writer of the stored
// state; callers hold the owner mutex.
func (s *mcpOwnedService) observe(state mcpState, detail string) {
	s.status.State, s.status.Detail = state, detail
}

// mcpOwner owns transport reuse across runs, not tool selection. Construction,
// status reads and borrowed views do no I/O. Only discovery opens connections;
// tool calls require the already discovered connection and never reconnect.
// All mutable service fields and slot accounting are protected by mu. Each
// service gate serializes its operations without holding mu during I/O.
type mcpOwner struct {
	mu            sync.Mutex
	configuration config.MCPConfig
	guard         *guard.Guard
	yolo          bool
	open          mcpOpenFunc
	refresh       mcpRefreshFunc
	services      map[string]*mcpOwnedService
	// approved and declined record in-run connection answers by key and
	// fingerprint; a changed connection asks again. approvalMu serializes prompts.
	approvalMu         sync.Mutex
	approved, declined map[string]string
	slots              int
	closed             bool
	work               sync.WaitGroup
	closeOnce          sync.Once
	closeErr           error
}

func newMCPOwner(configuration config.MCPConfig, gate *guard.Guard, yolo bool, open mcpOpenFunc, refresh mcpRefreshFunc) (*mcpOwner, error) {
	if gate == nil || len(configuration.Servers) > config.MaxMCPServices {
		return nil, fmt.Errorf("MCP owner requires a Guard and bounded configuration")
	}
	if open == nil {
		open = openMCPConnection
	}
	o := &mcpOwner{configuration: configuration.Clone(), guard: gate, yolo: yolo, open: open, refresh: refresh, services: make(map[string]*mcpOwnedService), approved: make(map[string]string), declined: make(map[string]string)}
	for _, key := range o.configuration.ServerKeys() {
		server := o.configuration.Servers[key]
		service := newMCPOwnedService(server)
		o.services[key] = service
		if err := gate.BindMCPService(guard.MCPService{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint, PermissionScope: mcpPermissionScope(o.configuration, key), Enabled: o.configuration.ServerAllowed(key)}); err != nil {
			for _, s := range o.services {
				s.cancel()
			}
			return nil, err
		}
	}
	return o, nil
}

func (o *mcpOwner) Connections() map[string]mcpCatalogConnection {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.connectionsLocked()
}

func (o *mcpOwner) connectionsLocked() map[string]mcpCatalogConnection {
	connections := make(map[string]mcpCatalogConnection, len(o.services))
	for key, service := range o.services {
		connections[key] = mcpBorrowedConnection{o, service}
	}
	return connections
}

func (o *mcpOwner) snapshot() (config.MCPConfig, map[string]mcpCatalogConnection) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.configuration.Clone(), o.connectionsLocked()
}

func newMCPOwnedService(server config.MCPServer) *mcpOwnedService {
	ctx, cancel := context.WithCancel(context.Background())
	return &mcpOwnedService{server: server, secrets: mcpConnectionSecrets(server), gate: make(chan struct{}, 1), ctx: ctx, cancel: cancel, generation: 1, resourceGeneration: 1, status: mcpServiceStatus{Key: server.Key, State: mcpDisconnected}}
}

// reconfigure is an idle settings publication. Unchanged services retain their
// transport and Guard epochs. Retired leases keep their canceled service object
// and redaction history; they can never follow a key into its replacement.
// Cleanup failures are reported separately from policy publication failures.
func (o *mcpOwner) reconfigure(configuration config.MCPConfig, reconnect ...string) (cleanupErr, err error) {
	configuration = configuration.Clone()
	o.mu.Lock()
	if o.closed || len(configuration.Servers) > config.MaxMCPServices {
		o.mu.Unlock()
		return nil, fmt.Errorf("MCP owner is closed or configuration exceeds its bound")
	}
	o.work.Add(1)
	defer o.work.Done()
	var clients []mcpOwnedConnection
	for key, service := range o.services {
		next, exists := configuration.Servers[key]
		if exists && !service.revoked && !slices.Contains(reconnect, key) &&
			reflect.DeepEqual(o.configuration.Servers[key], next) &&
			o.configuration.ConnectionDecision(key) == configuration.ConnectionDecision(key) &&
			mcpPermissionScope(o.configuration, key) == mcpPermissionScope(configuration, key) {
			continue
		}
		if client := o.retireLocked(service); client != nil {
			clients = append(clients, client)
		}
		o.guard.RemoveMCPService(service.server.Source.Kind+":"+service.server.Source.Location, service.server.ID)
		delete(o.services, key)
	}
	o.configuration = configuration
	// A saved allow is the user's later, explicit decision; it supersedes a
	// Deny answered during a run.
	for key := range o.declined {
		if configuration.ConnectionDecision(key) == config.MCPConnectionAllow {
			delete(o.declined, key)
		}
	}
	for _, key := range configuration.ServerKeys() {
		if o.services[key] != nil {
			continue
		}
		server := configuration.Servers[key]
		service := newMCPOwnedService(server)
		o.services[key] = service
		bindErr := o.guard.BindMCPService(guard.MCPService{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint, PermissionScope: mcpPermissionScope(configuration, key), Enabled: configuration.ServerAllowed(key)})
		if bindErr != nil {
			service.revoked = true
			service.cancel()
			err = errors.Join(err, bindErr)
		}
	}
	o.mu.Unlock()
	for _, client := range clients {
		cleanupErr = errors.Join(cleanupErr, client.Close())
		o.mu.Lock()
		o.slots--
		o.mu.Unlock()
	}
	return cleanupErr, err
}

func (o *mcpOwner) Status() []mcpServiceStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]mcpServiceStatus, 0, len(o.services))
	for _, key := range o.configuration.ServerKeys() {
		s := o.services[key]
		o.generationLocked(s)
		result = append(result, o.statusLocked(s))
	}
	return result
}

// statusLocked overlays the current policy and credential blocks on the
// observed connection state. It never changes the service.
func (o *mcpOwner) statusLocked(s *mcpOwnedService) mcpServiceStatus {
	status := s.status
	if state, detail := o.blockLocked(s); state != "" {
		status.State, status.Detail, status.CatalogKnown = state, detail, false
	} else if err := o.expiryLocked(s); err != nil {
		status.State, status.Detail = mcpNeedsAuth, err.Error()
	}
	return status
}

// blockLocked reports why connection authorization forbids work now,
// independently of expiry and tool grants. --yolo skips only Ask, never
// configuration or explicit user denies.
func (o *mcpOwner) blockLocked(s *mcpOwnedService) (mcpState, string) {
	switch {
	case o.closed:
		return mcpDisconnected, "MCP owner is closed"
	case s.revoked || !o.configuration.ServerAllowed(s.server.Key):
		return mcpDisabled, "MCP service is disabled or revoked"
	case !o.guard.MCPServiceAvailable(s.server.Source.Kind+":"+s.server.Source.Location, s.server.ID, s.server.Fingerprint, mcpPermissionScope(o.configuration, s.server.Key)):
		return mcpDisabled, "MCP service policy changed"
	case o.configuration.ConnectionDecision(s.server.Key) == config.MCPConnectionDeny:
		return mcpDisabled, "MCP connection is denied"
	case len(s.server.MissingValues) > 0:
		return mcpNeedsAuth, "MCP connection values are missing"
	case s.authFailed:
		return mcpNeedsAuth, "MCP OAuth refresh or authentication failed; inspect credentials and explicitly reconnect or log in"
	case o.declined[s.server.Key] == s.server.Fingerprint:
		return mcpDisabled, "MCP connection was declined while AICE is running"
	case !o.yolo && o.configuration.ConnectionDecision(s.server.Key) != config.MCPConnectionAllow && o.approved[s.server.Key] != s.server.Fingerprint:
		return mcpNeedsApproval, "MCP connection needs approval; interactive runs ask on first use"
	}
	return "", ""
}

func (o *mcpOwner) policyLocked(s *mcpOwnedService) error {
	if state, detail := o.blockLocked(s); state != "" {
		return errors.New(detail)
	}
	return nil
}

func (o *mcpOwner) expiryLocked(s *mcpOwnedService) error {
	if s.server.OAuthTokenExpired(time.Now()) {
		return errors.New("MCP OAuth token has expired; refresh is checked before authorized work")
	}
	return nil
}

func (o *mcpOwner) permissionLocked(s *mcpOwnedService) error {
	if err := o.policyLocked(s); err != nil {
		return err
	}
	return o.expiryLocked(s)
}

func (o *mcpOwner) begin(ctx context.Context, s *mcpOwnedService) (*mcpOwnedService, context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if err := o.approveIfNeeded(ctx, s); err != nil {
		return nil, nil, nil, err
	}
	o.mu.Lock()
	if s == nil {
		o.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("unknown MCP service")
	}
	if err := o.policyLocked(s); err != nil {
		o.mu.Unlock()
		return nil, nil, nil, err
	}
	o.work.Add(1)
	timeout := s.server.CallTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	stop := context.AfterFunc(s.ctx, cancel)
	o.mu.Unlock()
	release := func() { stop(); cancel(); o.work.Done() }
	select {
	case s.gate <- struct{}{}:
		done := func() { <-s.gate; release() }
		if err := o.prepareAuthentication(operation, s); err != nil {
			done()
			return nil, nil, nil, err
		}
		return s, operation, done, nil
	case <-operation.Done():
		release()
		return nil, nil, nil, operation.Err()
	}
}

// ensure runs with the service gate held. A failed connect releases its slot;
// only a later explicit discovery may try again. No tool action is replayed.
func (o *mcpOwner) ensure(ctx context.Context, s *mcpOwnedService) (mcpOwnedConnection, error) {
	o.mu.Lock()
	if err := ctx.Err(); err != nil {
		o.mu.Unlock()
		return nil, err
	}
	if err := o.permissionLocked(s); err != nil {
		o.mu.Unlock()
		return nil, err
	}
	if s.client != nil {
		client := s.client
		o.mu.Unlock()
		return client, nil
	}
	if o.slots >= 8 {
		s.observe(mcpDisconnected, "MCP connection limit reached (8); disconnect another service")
		o.mu.Unlock()
		return nil, fmt.Errorf("MCP connection limit reached")
	}
	o.slots++
	s.observe(mcpConnecting, "")
	o.mu.Unlock()
	_, headers := s.server.ConnectionValues()
	configuration := mcpclient.Config{ConnectTimeout: s.server.ConnectTimeout, CallTimeout: s.server.CallTimeout, Elicit: mcpElicit(s.server.Key)}
	if s.server.Settings.Transport == "stdio" {
		configuration.Stdio = mcpStdioConfiguration(s.server)
	} else {
		configuration.HTTP = &mcpclient.HTTPConfig{Endpoint: s.server.Settings.URL, Headers: headers}
		if s.server.Settings.OAuth != nil {
			delete(headers, "Authorization")
			configuration.HTTP.Authorization = func() string {
				o.mu.Lock()
				defer o.mu.Unlock()
				credential, _ := s.server.OAuthCredentials()
				return "Bearer " + credential.AccessToken
			}
		}
	}
	var client mcpOwnedConnection
	err := mcpLaunchReady(s.server)
	if err == nil {
		client, err = o.open(ctx, configuration)
	}
	o.mu.Lock()
	if err == nil {
		err = ctx.Err()
	}
	if policyErr := o.permissionLocked(s); err == nil {
		err = policyErr
	}
	if err == nil && client == nil {
		err = fmt.Errorf("MCP opener returned no connection")
	}
	if err != nil {
		if o.permissionLocked(s) == nil {
			s.observe(mcpFailureStatus(err))
			s.authFailed = s.server.Settings.OAuth != nil && mcpAuthenticationRejected(err)
		}
		o.mu.Unlock()
		if client != nil {
			_ = client.Close()
		}
		o.mu.Lock()
		o.slots--
		o.mu.Unlock()
		return nil, fmt.Errorf("MCP connection unavailable; inspect service status")
	}
	s.client = client
	s.generation++
	s.clientGeneration = client.ToolGeneration()
	s.resourceGeneration++
	if resource, ok := client.(mcpResourceConnection); ok {
		s.clientResourceGeneration = resource.ResourceGeneration()
	}
	s.observe(mcpReady, "")
	o.mu.Unlock()
	return client, nil
}

// mcpLaunchError is a local, already sanitized reason a stdio service cannot start.
type mcpLaunchError struct{ reason string }

func (e mcpLaunchError) Error() string { return e.reason }

func mcpLaunchReady(server config.MCPServer) error {
	if server.Settings.Transport != "stdio" {
		return nil
	}
	if server.Executable == "" {
		return mcpLaunchError{fmt.Sprintf("MCP command %q was not found on PATH when configuration loaded", server.Settings.Command)}
	}
	if server.WorkingDir == "" {
		return mcpLaunchError{"MCP stdio cwd is unset and no workspace is available"}
	}
	return nil
}

func mcpFailureStatus(err error) (mcpState, string) {
	var launch mcpLaunchError
	if errors.As(err, &launch) {
		return mcpFailed, launch.reason
	}
	if mcpAuthenticationRejected(err) {
		return mcpNeedsAuth, "MCP authentication is required or was rejected"
	}
	return mcpFailed, "MCP connection or discovery failed; remote details omitted"
}

func (o *mcpOwner) generationLocked(s *mcpOwnedService) uint64 {
	if s.client != nil {
		generation := s.client.ToolGeneration()
		if generation != s.clientGeneration {
			s.clientGeneration = generation
			s.generation++
			s.status.CatalogKnown = false
		}
	}
	return s.generation
}

// Revoke cancels this service's work and invalidates all borrowed versions
// before closing its owned connection. Restoring it requires a new service with
// an explicit policy publication; an old run cannot undo the revocation.
func (o *mcpOwner) Revoke(key string) error {
	o.mu.Lock()
	s := o.services[key]
	if s == nil || o.closed {
		o.mu.Unlock()
		return fmt.Errorf("MCP service is unavailable")
	}
	o.work.Add(1)
	client := o.retireLocked(s)
	o.mu.Unlock()
	defer o.work.Done()
	if client == nil {
		return nil
	}
	err := client.Close()
	o.mu.Lock()
	o.slots--
	o.mu.Unlock()
	return err
}

func (o *mcpOwner) retireLocked(s *mcpOwnedService) mcpOwnedConnection {
	s.revoked = true
	s.cancel()
	s.generation++
	s.resourceGeneration++
	o.guard.RevokeMCPService(s.server.Source.Kind+":"+s.server.Source.Location, s.server.ID)
	client := s.client
	s.client = nil
	s.status.ToolCount, s.status.CatalogKnown, s.status.EligibleTools = 0, false, 0
	return client
}

func (o *mcpOwner) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		for _, s := range o.services {
			s.cancel()
			s.generation++
			s.resourceGeneration++
		}
		o.mu.Unlock()
		o.work.Wait()
		// Openers that complete after cancellation close their own returned client.
		// No new operation or revocation can now claim a client.
		for _, s := range o.services {
			o.mu.Lock()
			client := s.client
			s.client = nil
			o.mu.Unlock()
			if client != nil {
				o.closeErr = errors.Join(o.closeErr, client.Close())
			}
		}
		o.mu.Lock()
		o.slots = 0
		o.mu.Unlock()
	})
	return o.closeErr
}

type mcpBorrowedConnection struct {
	owner   *mcpOwner
	service *mcpOwnedService
}

func (b mcpBorrowedConnection) ToolGeneration() uint64 {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	return b.owner.generationLocked(b.service)
}

func (b mcpBorrowedConnection) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	o := b.owner
	s, operation, release, err := o.begin(ctx, b.service)
	if err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	defer release()
	client, err := o.ensure(operation, s)
	if err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	catalog, err := client.Tools(operation)
	o.mu.Lock()
	defer o.mu.Unlock()
	if policyErr := o.permissionLocked(s); policyErr != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, policyErr
	}
	if errors.Is(err, mcpclient.ErrUnsupported) {
		s.observe(mcpReady, "MCP service does not advertise tools")
		s.status.CatalogKnown, s.status.ToolCount, s.status.EligibleTools = true, 0, 0
		return catalog, err
	}
	if err != nil {
		s.observe(mcpFailureStatus(err))
		s.authFailed = s.server.Settings.OAuth != nil && mcpAuthenticationRejected(err)
		s.status.CatalogKnown = false
		return catalog, err
	}
	if s.client != client || catalog.Generation != client.ToolGeneration() {
		return mcpclient.Catalog[mcpclient.Tool]{}, fmt.Errorf("MCP connection changed during discovery")
	}
	catalog.Generation = o.generationLocked(s)
	detail := ""
	if !catalog.Complete {
		detail = "MCP catalog is incomplete"
	}
	s.observe(mcpReady, detail)
	s.status.ToolCount, s.status.CatalogKnown, s.status.EligibleTools = len(catalog.Items), catalog.Complete, 0
	for _, remote := range catalog.Items {
		if o.configuration.ToolAllowed(s.server.Key, remote.Name) {
			s.status.EligibleTools++
		}
	}
	return catalog, nil
}

func (b mcpBorrowedConnection) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	s, operation, release, err := b.owner.begin(ctx, b.service)
	if err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	defer release()
	b.owner.mu.Lock()
	client := s.client
	b.owner.mu.Unlock()
	if client == nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, fmt.Errorf("MCP connection unavailable; discover tools again")
	}
	result, err := client.CallChecked(operation, name, args, b.dispatchCheck(s, client, check))
	b.owner.recordAuthenticationFailure(s, err)
	return result, err
}

// MCPStatus returns only application-owned status text, never remote errors or
// configured credentials. The catalog can explain an unavailable lazy lease.
func (b mcpBorrowedConnection) MCPStatus() string {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	status := b.owner.statusLocked(b.service)
	return string(status.State) + ": " + status.Detail
}

// A required connection need not expose tools (resource-only services are valid).
func (o *mcpOwner) prepareRequired(ctx context.Context, key string) error {
	o.mu.Lock()
	service := o.services[key]
	o.mu.Unlock()
	s, operation, release, err := o.begin(ctx, service)
	if err != nil {
		return err
	}
	defer release()
	_, err = o.ensure(operation, s)
	return err
}

func (a *application) newConfiguredMCPOwner(c config.Config, gate *guard.Guard, yolo bool) (*mcpOwner, error) {
	return newMCPOwner(c.MCP, gate, yolo, a.dependencies.openMCP, mcpOAuthRefresh(c.Paths))
}

func mcpStdioConfiguration(server config.MCPServer) *mcpclient.StdioConfig {
	if server.Settings.Transport != "stdio" {
		return nil
	}
	env, _ := server.ConnectionValues()
	return &mcpclient.StdioConfig{Executable: server.Executable, Args: slices.Clone(server.Settings.Args), Dir: server.WorkingDir, Env: env}
}
