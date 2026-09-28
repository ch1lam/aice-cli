package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type mcpServiceStatus struct {
	Key, State, Detail string
	ToolCount          int
	CatalogKnown       bool
	EligibleTools      int
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
	status                                       mcpServiceStatus
}

// mcpOwner owns transport reuse across runs, not tool selection. Construction,
// status reads and borrowed views do no I/O. Only discovery opens connections;
// tool calls require the already discovered connection and never reconnect.
// All mutable service fields and slot accounting are protected by mu. Each
// service gate serializes its operations without holding mu during I/O.
type mcpOwner struct {
	mu              sync.Mutex
	configuration   config.MCPConfig
	guard           *guard.Guard
	yolo            bool
	open            mcpOpenFunc
	checkConnection func(mcpclient.Config) error
	refresh         mcpRefreshFunc
	services        map[string]*mcpOwnedService
	slots           int
	closed          bool
	work            sync.WaitGroup
	closeOnce       sync.Once
	closeErr        error
}

func newMCPOwner(configuration config.MCPConfig, gate *guard.Guard, yolo bool, open mcpOpenFunc, refresh mcpRefreshFunc) (*mcpOwner, error) {
	if gate == nil || len(configuration.Servers) > 129 {
		return nil, fmt.Errorf("MCP owner requires a Guard and bounded configuration")
	}
	if open == nil {
		open = openMCPConnection
	}
	o := &mcpOwner{configuration: configuration.Clone(), guard: gate, yolo: yolo, open: open, refresh: refresh, services: make(map[string]*mcpOwnedService)}
	for _, key := range o.configuration.ServerKeys() {
		server := o.configuration.Servers[key]
		ctx, cancel := context.WithCancel(context.Background())
		service := &mcpOwnedService{server: server, secrets: mcpConnectionSecrets(server), gate: make(chan struct{}, 1), ctx: ctx, cancel: cancel, generation: 1, resourceGeneration: 1, status: mcpServiceStatus{Key: key, State: "disconnected"}}
		o.services[key] = service
		if err := gate.BindMCPService(guard.MCPService{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint, PermissionScope: mcpPermissionScope(o.configuration, key), Enabled: o.configuration.ServerAllowed(key)}); err != nil {
			for _, s := range o.services {
				s.cancel()
			}
			return nil, err
		}
		o.permissionLocked(service)
	}
	return o, nil
}

func (o *mcpOwner) Connections() map[string]mcpCatalogConnection {
	connections := make(map[string]mcpCatalogConnection, len(o.services))
	for key := range o.services {
		connections[key] = mcpBorrowedConnection{o, key}
	}
	return connections
}

func (o *mcpOwner) Status() []mcpServiceStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]mcpServiceStatus, 0, len(o.services))
	for _, key := range o.configuration.ServerKeys() {
		s := o.services[key]
		o.generationLocked(s)
		o.permissionLocked(s)
		result = append(result, s.status)
	}
	return result
}

// policyLocked checks connection authorization independently of expiry and tool
// grants. --yolo skips only Ask, never configuration or explicit user denies.
func (o *mcpOwner) policyLocked(s *mcpOwnedService) error {
	state, detail := "", ""
	switch {
	case o.closed:
		state, detail = "disconnected", "MCP owner is closed"
	case s.revoked || !o.configuration.ServerAllowed(s.server.Key):
		state, detail = "disabled", "MCP service is disabled or revoked"
	case !o.guard.MCPServiceAvailable(s.server.Source.Kind+":"+s.server.Source.Location, s.server.ID, s.server.Fingerprint, mcpPermissionScope(o.configuration, s.server.Key)):
		state, detail = "disabled", "MCP service policy changed"
	case o.configuration.ConnectionDecision(s.server.Key) == config.MCPConnectionDeny:
		state, detail = "disabled", "MCP connection is denied"
	case len(s.server.MissingValues) > 0:
		state, detail = "needs_auth", "MCP connection values are missing"
	case s.authFailed:
		state, detail = "needs_auth", "MCP OAuth refresh or authentication failed; inspect credentials and explicitly reconnect or log in"
	case !o.yolo && o.configuration.ConnectionDecision(s.server.Key) != config.MCPConnectionAllow:
		state, detail = "needs_approval", "MCP connection needs explicit approval"
	}
	if state == "" {
		return nil
	}
	s.status.State, s.status.Detail = state, detail
	s.status.CatalogKnown = false
	return errors.New(detail)
}

func (o *mcpOwner) permissionLocked(s *mcpOwnedService) error {
	if err := o.policyLocked(s); err != nil {
		return err
	}
	if s.server.OAuthTokenExpired(time.Now()) {
		s.status.State, s.status.Detail = "needs_auth", "MCP OAuth token has expired; refresh is checked before authorized work"
		return errors.New(s.status.Detail)
	}
	return nil
}

func (o *mcpOwner) begin(ctx context.Context, key string) (*mcpOwnedService, context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	o.mu.Lock()
	s := o.services[key]
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
		s.status.State, s.status.Detail = "disconnected", "MCP connection limit reached (8); disconnect another service"
		o.mu.Unlock()
		return nil, fmt.Errorf("MCP connection limit reached")
	}
	o.slots++
	s.status.State, s.status.Detail = "connecting", ""
	o.mu.Unlock()
	_, headers := s.server.ConnectionValues()
	configuration := mcpclient.Config{ConnectTimeout: s.server.ConnectTimeout, CallTimeout: s.server.CallTimeout}
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
	client, err := o.open(ctx, configuration)
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
			s.status.State, s.status.Detail = mcpFailureStatus(err)
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
	s.status.State, s.status.Detail = "ready", ""
	o.mu.Unlock()
	return client, nil
}

func mcpFailureStatus(err error) (string, string) {
	if errors.Is(err, errManagedCUAConnection) {
		return "disabled", errManagedCUAConnection.Error()
	}
	var httpError *mcpclient.HTTPError
	if errors.As(err, &httpError) && (httpError.StatusCode == 401 || httpError.StatusCode == 403) {
		return "needs_auth", "MCP authentication is required or was rejected"
	}
	return "failed", "MCP connection or discovery failed; remote details omitted"
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
// before closing its owned connection. Restoring it requires a new owner with
// an explicit policy publication; an old run cannot undo the revocation.
func (o *mcpOwner) Revoke(key string) error {
	o.mu.Lock()
	s := o.services[key]
	if s == nil || o.closed {
		o.mu.Unlock()
		return fmt.Errorf("MCP service is unavailable")
	}
	o.work.Add(1)
	s.revoked = true
	s.cancel()
	s.generation++
	s.resourceGeneration++
	o.guard.RevokeMCPService(s.server.Source.Kind+":"+s.server.Source.Location, s.server.ID)
	client := s.client
	s.client = nil
	s.status.State, s.status.Detail, s.status.ToolCount = "disabled", "MCP service was revoked", 0
	s.status.CatalogKnown, s.status.EligibleTools = false, 0
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
	owner *mcpOwner
	key   string
}

func (b mcpBorrowedConnection) ToolGeneration() uint64 {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	return b.owner.generationLocked(b.owner.services[b.key])
}

func (b mcpBorrowedConnection) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	o := b.owner
	s, operation, release, err := o.begin(ctx, b.key)
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
		s.status.State, s.status.Detail = "ready", "MCP service does not advertise tools"
		s.status.CatalogKnown, s.status.ToolCount, s.status.EligibleTools = true, 0, 0
		return catalog, err
	}
	if err != nil {
		s.status.State, s.status.Detail = mcpFailureStatus(err)
		s.authFailed = s.server.Settings.OAuth != nil && mcpAuthenticationRejected(err)
		s.status.CatalogKnown = false
		return catalog, err
	}
	if s.client != client || catalog.Generation != client.ToolGeneration() {
		return mcpclient.Catalog[mcpclient.Tool]{}, fmt.Errorf("MCP connection changed during discovery")
	}
	catalog.Generation = o.generationLocked(s)
	s.status.State, s.status.Detail, s.status.ToolCount = "ready", "", len(catalog.Items)
	s.status.CatalogKnown, s.status.EligibleTools = catalog.Complete, 0
	for _, remote := range catalog.Items {
		if o.configuration.ToolAllowed(s.server.Key, remote.Name) {
			s.status.EligibleTools++
		}
	}
	if !catalog.Complete {
		s.status.Detail = "MCP catalog is incomplete"
	}
	return catalog, nil
}

func (b mcpBorrowedConnection) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	s, operation, release, err := b.owner.begin(ctx, b.key)
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
	s := b.owner.services[b.key]
	b.owner.permissionLocked(s)
	return s.status.State + ": " + s.status.Detail
}

// A required connection need not expose tools (resource-only services are valid).
func (o *mcpOwner) prepareRequired(ctx context.Context, key string) error {
	s, operation, release, err := o.begin(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	_, err = o.ensure(operation, s)
	return err
}
