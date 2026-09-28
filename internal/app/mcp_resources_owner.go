package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// Resource capability is independent of tools. Old or specialized connection
// implementations can explicitly report unsupported without inventing a catalog.
type mcpResourceConnection interface {
	Resources(context.Context) (mcpclient.Catalog[mcpclient.Resource], error)
	ResourceGeneration() uint64
	ReadResourceChecked(context.Context, string, func(context.Context) error) (mcpclient.Result, error)
}

func (o *mcpOwner) resourceGenerationLocked(s *mcpOwnedService) uint64 {
	if client, ok := s.client.(mcpResourceConnection); ok {
		generation := client.ResourceGeneration()
		if generation != s.clientResourceGeneration {
			s.clientResourceGeneration = generation
			s.resourceGeneration++
		}
	}
	return s.resourceGeneration
}
func (b mcpBorrowedConnection) ResourceGeneration() uint64 {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	return b.owner.resourceGenerationLocked(b.owner.services[b.key])
}
func (b mcpBorrowedConnection) Resources(ctx context.Context) (mcpclient.Catalog[mcpclient.Resource], error) {
	o := b.owner
	s, operation, release, err := o.begin(ctx, b.key)
	if err != nil {
		return mcpclient.Catalog[mcpclient.Resource]{}, err
	}
	defer release()
	client, err := o.ensure(operation, s)
	if err != nil {
		return mcpclient.Catalog[mcpclient.Resource]{}, err
	}
	resource, ok := client.(mcpResourceConnection)
	if !ok {
		return mcpclient.Catalog[mcpclient.Resource]{}, mcpclient.ErrUnsupported
	}
	catalog, err := resource.Resources(operation)
	o.mu.Lock()
	defer o.mu.Unlock()
	if policyErr := o.permissionLocked(s); policyErr != nil {
		return mcpclient.Catalog[mcpclient.Resource]{}, policyErr
	}
	if err != nil {
		if !errors.Is(err, mcpclient.ErrUnsupported) {
			s.status.State, s.status.Detail = mcpFailureStatus(err)
			s.authFailed = s.server.Settings.OAuth != nil && mcpAuthenticationRejected(err)
		}
		return catalog, err
	}
	if s.client != client || catalog.Generation != resource.ResourceGeneration() {
		return mcpclient.Catalog[mcpclient.Resource]{}, fmt.Errorf("MCP resources changed during discovery")
	}
	catalog.Generation = o.resourceGenerationLocked(s)
	s.status.State, s.status.Detail = "ready", ""
	if !catalog.Complete {
		s.status.Detail = "MCP resource catalog is incomplete"
	}
	return catalog, nil
}
func (b mcpBorrowedConnection) ReadResourceChecked(ctx context.Context, uri string, check func(context.Context) error) (mcpclient.Result, error) {
	s, operation, release, err := b.owner.begin(ctx, b.key)
	if err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	defer release()
	b.owner.mu.Lock()
	client := s.client
	b.owner.mu.Unlock()
	resource, ok := client.(mcpResourceConnection)
	if !ok {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, mcpclient.ErrUnsupported
	}
	result, err := resource.ReadResourceChecked(operation, uri, b.dispatchCheck(s, client, check))
	b.owner.recordAuthenticationFailure(s, err)
	return result, err
}

// The transport calls this only after acquiring its queue. Owner permissions
// and the Loop's call-local grant are both rechecked at that final boundary.
func (b mcpBorrowedConnection) dispatchCheck(s *mcpOwnedService, client mcpOwnedConnection, check func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.owner.mu.Lock()
		err := b.owner.permissionLocked(s)
		if err == nil && s.client != client {
			err = fmt.Errorf("MCP connection changed before dispatch")
		}
		b.owner.mu.Unlock()
		if err != nil {
			return err
		}
		if check == nil {
			return fmt.Errorf("MCP dispatch check is required")
		}
		return check(ctx)
	}
}
