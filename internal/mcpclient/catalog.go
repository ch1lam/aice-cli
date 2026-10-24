package mcpclient

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxCatalogTTL bounds a server's freshness hint for a complete catalog.
const maxCatalogTTL = 10 * time.Minute

type catalogPage[T any] struct {
	items    []T
	next     string
	ttl      time.Duration
	received receipt
}

// catalogCache keeps one complete catalog for its server-advertised ttlMs.
// A list_changed notification advances the generation and invalidates it; a
// result without ttlMs (older protocols) is never cached.
type catalogCache[T any] struct {
	mu      sync.Mutex
	catalog Catalog[T]
	expires time.Time
}

func (c *catalogCache[T]) get(generation uint64, now time.Time) (Catalog[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.catalog.Complete || c.catalog.Generation != generation || !now.Before(c.expires) {
		return Catalog[T]{}, false
	}
	copy := c.catalog
	copy.Items = slices.Clone(c.catalog.Items)
	return copy, true
}

func (c *catalogCache[T]) put(catalog Catalog[T], ttl time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !catalog.Complete || ttl <= 0 {
		c.catalog, c.expires = Catalog[T]{}, time.Time{}
		return
	}
	c.catalog, c.expires = catalog, now.Add(min(ttl, maxCatalogTTL))
	c.catalog.Items = slices.Clone(catalog.Items)
}

func pageTTL(ms int) time.Duration { return time.Duration(max(ms, 0)) * time.Millisecond }

func (c *Client) Tools(ctx context.Context) (Catalog[Tool], error) {
	if !c.info.Tools {
		return Catalog[Tool]{Complete: true, Generation: c.ToolGeneration()}, ErrUnsupported
	}
	return listCatalog(ctx, c, &c.toolGeneration, &c.toolCache,
		func(ctx context.Context, cursor string) (catalogPage[Tool], error) {
			c.receipts.begin("tools/list")
			page, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			result := catalogPage[Tool]{received: c.receipts.finish()}
			if page == nil {
				return result, err
			}
			// This client caches exact wire schemas itself; the SDK's decoded
			// cache would round them, so it never serves this page again.
			result.ttl, page.TTLMs = pageTTL(page.TTLMs), 0
			if err != nil {
				return result, err
			}
			schemas := result.received.schemas
			// The SDK may filter invalid tools. Fail the page instead of pairing
			// the remaining tools with another tool's exact schema.
			if schemas == nil || len(schemas) != len(page.Tools) {
				return result, ErrProtocol
			}
			result.next = page.NextCursor
			result.items = make([]Tool, 0, len(page.Tools))
			for i, tool := range page.Tools {
				var annotations json.RawMessage
				if tool.Annotations != nil {
					annotations, err = json.Marshal(tool.Annotations)
					if err != nil {
						return result, err
					}
				}
				result.items = append(result.items, Tool{Name: tool.Name, Title: tool.Title,
					Description: tool.Description, InputSchema: schemas[i].InputSchema,
					OutputSchema: schemas[i].OutputSchema, Annotations: annotations})
			}
			return result, nil
		}, func(tool Tool) (string, bool) {
			return tool.Name, validName(tool.Name) && jsonObject(tool.InputSchema) &&
				(len(tool.OutputSchema) == 0 || jsonObject(tool.OutputSchema))
		})
}

func (c *Client) Resources(ctx context.Context) (Catalog[Resource], error) {
	if !c.info.Resources {
		return Catalog[Resource]{Complete: true, Generation: c.ResourceGeneration()}, ErrUnsupported
	}
	return listCatalog(ctx, c, &c.resourceGeneration, &c.resourceCache,
		func(ctx context.Context, cursor string) (catalogPage[Resource], error) {
			c.receipts.begin("resources/list")
			page, err := c.session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			result := catalogPage[Resource]{received: c.receipts.finish()}
			if page == nil {
				return result, err
			}
			result.ttl, page.TTLMs = pageTTL(page.TTLMs), 0
			if err != nil || page.Resources == nil {
				return result, ErrProtocol
			}
			result.next = page.NextCursor
			result.items = make([]Resource, 0, len(page.Resources))
			for _, resource := range page.Resources {
				if resource == nil {
					return result, ErrProtocol
				}
				result.items = append(result.items, Resource{URI: resource.URI, Name: resource.Name,
					Title: resource.Title, Description: resource.Description, MIMEType: resource.MIMEType})
			}
			return result, nil
		}, func(resource Resource) (string, bool) {
			return resource.URI, validName(resource.URI) && validName(resource.Name)
		})
}

func listCatalog[T any](ctx context.Context, c *Client, generation *atomic.Uint64, cache *catalogCache[T],
	fetch func(context.Context, string) (catalogPage[T], error), identity func(T) (string, bool),
) (Catalog[T], error) {
	if cached, ok := cache.get(generation.Load(), time.Now()); ok {
		return cached, ctx.Err()
	}
	result := Catalog[T]{Generation: generation.Load()}
	ctx, release, err := c.acquire(ctx)
	if err != nil {
		result.Notice = "Catalog unavailable; connection or request was canceled."
		return result, err
	}
	defer release()
	result.Generation = generation.Load()
	cursor, size, ttl := "", 0, time.Duration(-1)
	cursors, names := make(map[string]bool), make(map[string]bool)
	for range c.limits.Pages {
		page, fetchErr := fetch(ctx, cursor)
		received := page.received
		if fetchErr != nil || !received.returned || received.rpcError || received.limited {
			result.Notice = "Catalog incomplete; a page could not be read."
			return result, operationError(ctx, received, fetchErr)
		}
		size += received.resultBytes
		if size > c.limits.CatalogBytes {
			result.Notice = "Catalog byte limit reached; narrow the server's exported catalog."
			return result, nil
		}
		if page.items == nil {
			result.Notice = "Catalog incomplete; the server returned an invalid page."
			return result, ErrProtocol
		}
		cursor = page.next
		if ttl < 0 || page.ttl < ttl {
			ttl = page.ttl
		}
		for _, item := range page.items {
			name, valid := identity(item)
			if !valid || names[name] {
				result.Notice = "Catalog incomplete; an entry was invalid or had a duplicate identity."
				return result, ErrProtocol
			}
			if len(result.Items) == c.limits.CatalogItems {
				result.Notice = "Catalog item limit reached; narrow the server's exported catalog."
				return result, nil
			}
			names[name] = true
			result.Items = append(result.Items, item)
		}
		if generation.Load() != result.Generation {
			result.Notice = "Catalog changed during discovery; refresh before selecting tools."
			return result, nil
		}
		if cursor == "" {
			result.Complete = true
			cache.put(result, ttl, time.Now())
			return result, nil
		}
		if cursors[cursor] {
			result.Notice = "Catalog incomplete; the server repeated a pagination cursor."
			return result, ErrProtocol
		}
		cursors[cursor] = true
	}
	result.Notice = "Catalog page limit reached; narrow the server's exported catalog."
	return result, nil
}
