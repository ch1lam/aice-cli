package llm

import "context"

type sessionIDKey struct{}

// WithSessionID attaches an opaque conversation identity for provider routing.
// The application owns its lifetime; it is not part of the model prompt.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionID returns the conversation routing identity, or empty if absent.
func SessionID(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}
