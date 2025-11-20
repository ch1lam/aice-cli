# Repository Guidelines

## Architecture & Project Structure
TypeScript code lives in `src`. Commands stay under `src/commands`; the MVP chat entry (`src/commands/chat.ts`) hands off to `ChatController` + `src/core/session` + `src/core/stream` for orchestration and streaming. That layer already tracks chunk ordering (meta → text → usage → done) and assigns sequential indexes, so providers only need to emit raw tokens/usage events. Provider adapters belong in `src/providers/{anthropic,openai,deepseek}.ts`, each wrapping the official SDK behind one shared interface; `src/providers/factory.ts` builds provider bindings. `src/providers/openai.ts` is the reference implementation: it constructs async generators around the Responses streaming API and maps every delta/usage/status/error event into the shared chunk format. CLI presentation details live in `src/commands/chat-runner.ts`, which converts session chunks into stdout writes/logs; keep Ink-specific rendering separate so we can swap in the TUI without touching providers. Configuration helpers (env parsing, provider selection) live in `src/config`. Ink components (ChatWindow, StatusBar, InputPanel) live in `src/ui`. Runtime shims in `bin/run.js` / `bin/dev.js` load the compiled `dist` bundle, so treat `dist` as read-only. Mirror this layout under `test/`.

## Build, Test & Development Commands
`yarn build` removes `dist` and runs `tsc -b`; run it whenever command signatures change. `yarn test` runs Mocha with the ESM ts-node loader (`TS_NODE_PROJECT=test/tsconfig.json node --loader ts-node/esm ./node_modules/mocha/bin/mocha --forbid-only "test/**/*.test.ts"`) and then `yarn lint` (you can also run `yarn lint` directly to iterate on ESLint issues). Stream the MVP locally with `AICE_PROVIDER=openai node bin/dev.js chat --model gpt-4o-mini`; swap the env var once other adapters ship. Before publishing, `yarn prepack` refreshes the manifest and README.

## Coding Style & Naming Conventions
Stick to ECMAScript modules, 2-space indentation, and explicit exports. Command IDs remain kebab-cased (`chat stream` -> `src/commands/chat/stream.ts`). Provider classes use PascalCase (`OpenAIProvider`), expose async generators for chunked output, and accept SDK clients via constructor injection. Ink components are PascalCase functions; shared hooks (like `useSession`) live in `src/ui/hooks`. Keep side effects inside `Command.run()` or focused services so UI and provider logic stay testable.

Avoid `any`. If a type truly isn’t available, prefer defining a concrete interface/type or fall back to `unknown`; when `unknown` is introduced, call it out in your change note/response (no PR required just for that).

## Testing Guidelines
Use Mocha + Chai + `@oclif/test` for commands, placing suites next to their modules (e.g., `test/commands/chat.test.ts`). Provider tests should stub SDK calls (sinon or hand-rolled fakes) and assert chunk ordering plus error propagation. Cover Ink components with `ink-testing-library`, ensuring streaming renders incrementally and the status bar mirrors provider + token usage. Add regression tests for failure modes such as invalid API keys, timeouts, or provider mismatches.

## Commit & Pull Request Guidelines
Commits favor short imperative subjects; add Conventional Commit prefixes only when they clarify scope. PRs must describe the scenario, list which providers were exercised, and attach terminal recordings or GIFs whenever the Ink UI changes. Always note the dev command you ran, confirm `yarn build && yarn test`, and link the relevant issue or follow-up tasks.

## Provider Configuration & Security
Load configuration from `.env` but keep secrets out of Git. For the MVP only `openai` is supported: require `AICE_PROVIDER=openai` and `AICE_OPENAI_API_KEY`, with optional overrides `AICE_OPENAI_BASE_URL` and `AICE_MODEL`; validate inputs before instantiating SDK clients and display actionable errors in the status bar. Gate verbose HTTP tracing behind `DEBUG` and redact prompt text in logs. When new providers land, add their keys (`AICE_ANTHROPIC_API_KEY`, `AICE_DEEPSEEK_API_KEY`) and wire selection through the provider factory.

## Planning & Tracking
Maintain a living `TODO.md` at the repo root. Every major phase (dependencies, session layer, providers, Ink UI, Agents integration) should have a checkbox entry, updated as work progresses. When plans change, edit both `TODO.md` and this guide so contributors always know the current roadmap and documentation expectations.
