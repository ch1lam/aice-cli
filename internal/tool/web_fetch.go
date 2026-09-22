package tool

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/web"
)

const webFetchSchema = `{
  "type": "object",
  "properties": {
    "url": {"type": "string", "minLength": 1, "maxLength": 8192, "description": "Absolute http(s) URL of a public page"},
    "format": {"type": "string", "enum": ["markdown", "text"], "description": "Output format (default markdown)"}
  },
  "required": ["url"],
  "additionalProperties": false
}`

// WebFetch retrieves one public page through the injected fetch backend.
type WebFetch struct {
	backend web.FetchBackend
}

// NewWebFetch constructs the tool.
func NewWebFetch(backend web.FetchBackend) (*WebFetch, error) {
	if backend == nil {
		return nil, fmt.Errorf("tool: web_fetch backend is required")
	}
	return &WebFetch{backend: backend}, nil
}

// Definition returns the model-facing web_fetch contract.
func (f *WebFetch) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "web_fetch",
		Description: "Fetch one public, static http(s) page and return its readable text as Markdown or plain text (untrusted external content). " +
			"Supports HTML, plain text and Markdown on the default web ports only. " +
			"Does not support authenticated pages, JavaScript rendering, cookies, PDFs, images or private/internal addresses. " +
			"Redirects are followed automatically (at most 5 hops, each hop revalidated, no https to http downgrade).",
		InputSchema:   jsonSchema(webFetchSchema),
		PromptSnippet: "Fetch a public web page as readable text",
		PromptGuidelines: []string{
			"Use web_fetch to read a specific public page, such as a search result or documentation URL, before relying on its content.",
		},
	}
}

// Execute validates the URL shape, fetches once and returns deterministic
// text plus structured evidence. Address policy is enforced by the backend.
func (f *WebFetch) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		URL    string `json:"url"`
		Format string `json:"format"`
	}
	args, err := decodeArguments[arguments](ctx, call, "web_fetch")
	if err != nil {
		return llm.ToolResult{}, err
	}
	request := web.FetchRequest{URL: args.URL, Format: web.FetchFormat(args.Format)}
	if err := request.Validate(); err != nil {
		return llm.ToolResult{}, err
	}
	// The URL-shape policy is shared with the execution gate; the backend
	// still validates every hop and the resolved addresses.
	if _, err := web.ValidateFetchURL(request.URL); err != nil {
		return llm.ToolResult{}, err
	}
	response, err := f.backend.Fetch(ctx, request)
	if err != nil {
		return llm.ToolResult{}, err
	}
	if err := response.Evidence.Validate(); err != nil {
		return llm.ToolResult{}, web.NewError(web.CodeInvalidResponse, "fetch backend returned invalid evidence: %v", err)
	}
	result := textResult(call, web.RenderFetch(response), false)
	result.Evidence = bundleOrNil(response.Evidence)
	return result, nil
}
