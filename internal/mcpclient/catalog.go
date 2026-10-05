package mcpclient

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type catalogPage[T any] struct {
	items    []T
	next     string
	received receipt
}

func (c *Client) Tools(ctx context.Context) (Catalog[Tool], error) {
	if !c.info.Tools {
		return Catalog[Tool]{Complete: true, Generation: c.ToolGeneration()}, ErrUnsupported
	}
	return listCatalog(ctx, c, &c.toolGeneration,
		func(ctx context.Context, cursor string) (catalogPage[Tool], error) {
			c.receipts.begin("tools/list")
			page, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			result := catalogPage[Tool]{received: c.receipts.finish()}
			if page == nil {
				return result, err
			}
			// App owns freshness. Never reuse decoded schemas rounded by the SDK.
			page.TTLMs = 0
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
	return listCatalog(ctx, c, &c.resourceGeneration,
		func(ctx context.Context, cursor string) (catalogPage[Resource], error) {
			c.receipts.begin("resources/list")
			page, err := c.session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			result := catalogPage[Resource]{received: c.receipts.finish()}
			if page == nil {
				return result, err
			}
			page.TTLMs = 0
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

func listCatalog[T any](ctx context.Context, c *Client, generation *atomic.Uint64,
	fetch func(context.Context, string) (catalogPage[T], error), identity func(T) (string, bool),
) (Catalog[T], error) {
	result := Catalog[T]{Generation: generation.Load()}
	ctx, release, err := c.acquire(ctx)
	if err != nil {
		result.Notice = "Catalog unavailable; connection or request was canceled."
		return result, err
	}
	defer release()
	result.Generation = generation.Load()
	cursor, size := "", 0
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
