# Repository Guidelines

## Architecture & Project Structure
TypeScript code lives in `src`. Commands stay under `src/commands`; the MVP chat entry (`src/commands/chat.ts`) should hand off to `src/core/session` + `src/core/stream` for orchestration and streaming. Provider adapters belong in `src/providers/{anthropic,openai,deepseek}.ts`, each wrapping the official SDK behind one shared interface. Ink components (ChatWindow, StatusBar, InputPanel) live in `src/ui`. Runtime shims in `bin/run.js` / `bin/dev.js` load the compiled `dist` bundle, so treat `dist` as read-only. Mirror this layout under `test/`.

## Build, Test & Development Commands
`yarn build` removes `dist` and runs `tsc -b`; run it whenever command signatures change. `yarn test` executes `mocha --forbid-only "test/**/*.test.ts"` via `ts-node`, then calls `yarn lint` (you can also run `yarn lint` directly to iterate on ESLint issues). Stream the MVP locally with `AICE_PROVIDER=openai node bin/dev.js chat --model gpt-4o-mini`; swap the env var once other adapters ship. Before publishing, `yarn prepack` refreshes the manifest and README.

## Coding Style & Naming Conventions
Stick to ECMAScript modules, 2-space indentation, and explicit exports. Command IDs remain kebab-cased (`chat stream` -> `src/commands/chat/stream.ts`). Provider classes use PascalCase (`OpenAIProvider`), expose async generators for chunked output, and accept SDK clients via constructor injection. Ink components are PascalCase functions; shared hooks (like `useSession`) live in `src/ui/hooks`. Keep side effects inside `Command.run()` or focused services so UI and provider logic stay testable.

## Testing Guidelines
Use Mocha + Chai + `@oclif/test` for commands, placing suites next to their modules (e.g., `test/commands/chat.test.ts`). Provider tests should stub SDK calls (sinon or hand-rolled fakes) and assert chunk ordering plus error propagation. Cover Ink components with `ink-testing-library`, ensuring streaming renders incrementally and the status bar mirrors provider + token usage. Add regression tests for failure modes such as invalid API keys, timeouts, or provider mismatches.

## Commit & Pull Request Guidelines
Commits favor short imperative subjects; add Conventional Commit prefixes only when they clarify scope. PRs must describe the scenario, list which providers were exercised, and attach terminal recordings or GIFs whenever the Ink UI changes. Always note the dev command you ran, confirm `yarn build && yarn test`, and link the relevant issue or follow-up tasks.

## Provider Configuration & Security
Load configuration from `.env` but keep secrets out of Git. Required keys: `AICE_PROVIDER`, `AICE_OPENAI_API_KEY`, `AICE_ANTHROPIC_API_KEY`, `AICE_DEEPSEEK_API_KEY`; optional overrides include `AICE_MODEL` or `AICE_TIMEOUT_MS`. Validate inputs before instantiating SDK clients and display actionable errors in the status bar. Gate verbose HTTP tracing behind `DEBUG` and redact prompt text in logs.
