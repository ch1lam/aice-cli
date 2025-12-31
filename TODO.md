# TODO

- [x] Document AI SDK migration scope, DeepSeek-only focus, and new env vars.
- [x] Update dependencies to Vercel AI SDK + DeepSeek provider packages; remove OpenAI SDK.
- [x] Replace provider registry/streaming adapters with AI SDK models and stream events.
- [x] Redesign session/stream types around AI SDK message and stream primitives.
- [x] Simplify setup flow and slash commands for single provider (drop /provider).
- [x] Update TUI shell + status bar to new stream/status/usage events.
- [x] Rework .env load/persist to new variable names and defaults.
- [x] Rebuild connectivity checks using AI SDK primitives.
- [x] Update tests for provider, session, UI, and config behavior.
- [x] Refresh docs after implementation and verify `yarn build && yarn test`.
