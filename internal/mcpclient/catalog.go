package mcpclient

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (c *Client) Tools(ctx context.Context) (Catalog[Tool], error) {
	if !c.info.Tools {
		return Catalog[Tool]{Complete: true, Generation: c.ToolGeneration()}, ErrUnsupported
	}
	return listCatalog(ctx, c, &c.toolGeneration, "tools/list", "tools",
		func(ctx context.Context, cursor string) error {
			_, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			return err
		}, func(tool Tool) (string, bool) {
			return tool.Name, validName(tool.Name) && jsonObject(tool.InputSchema) &&
				(len(tool.OutputSchema) == 0 || jsonObject(tool.OutputSchema))
		})
}

func (c *Client) Resources(ctx context.Context) (Catalog[Resource], error) {
	if !c.info.Resources {
		return Catalog[Resource]{Complete: true, Generation: c.ResourceGeneration()}, ErrUnsupported
	}
	return listCatalog(ctx, c, &c.resourceGeneration, "resources/list", "resources",
		func(ctx context.Context, cursor string) error {
			_, err := c.session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			return err
		}, func(resource Resource) (string, bool) {
			return resource.URI, validName(resource.URI) && validName(resource.Name)
		})
}

func listCatalog[T any](ctx context.Context, c *Client, generation *atomic.Uint64,
	method, key string, fetch func(context.Context, string) error, identity func(T) (string, bool),
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
		c.receipts.begin(method)
		fetchErr := fetch(ctx, cursor)
		received := c.receipts.finish()
		if len(received.result) == 0 || received.rpcError {
			result.Notice = "Catalog incomplete; a page could not be read."
			return result, operationError(ctx, received, fetchErr)
		}
		size += len(received.result)
		if size > c.limits.CatalogBytes {
			result.Notice = "Catalog byte limit reached; narrow the server's exported catalog."
			return result, nil
		}
		var page map[string]json.RawMessage
		var items []T
		if json.Unmarshal(received.result, &page) != nil || json.Unmarshal(page[key], &items) != nil ||
			items == nil || json.Unmarshal(defaultCursor(page["nextCursor"]), &cursor) != nil {
			result.Notice = "Catalog incomplete; the server returned an invalid page."
			return result, ErrProtocol
		}
		for _, item := range items {
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

func defaultCursor(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`""`)
	}
	return raw
}
