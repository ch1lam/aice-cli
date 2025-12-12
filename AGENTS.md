# Repository Guidelines

## Guiding Principles
- Keep behavior stable: refactor in small steps with fast tests as your safety net.
- Prefer clarity: readable names, short single-purpose functions, comments only for “why”.
- Spot smells early (duplication, long functions, god objects, long parameter lists, over-coupling) and fix with targeted refactors instead of big rewrites.
- Keep side effects contained in command runners/services so providers and UI stay testable.

## Architecture & Project Structure
- TypeScript lives in `src`; oclif commands stay under `src/commands`. The default `aice` (no args) launches the Ink TUI chat shell (`tui`).
- Scriptable chat orchestration lives in `src/chat/controller.ts` + `src/chat/prompt.ts`; these hand off to `src/core/session.ts` + `src/core/stream.ts` for chunk ordering/streaming. The session layer orders chunks (meta → text → usage → done) and assigns indexes; providers only emit raw tokens/usage.
- Provider adapters sit in `src/providers/{openai,deepseek}.ts`, each wrapping the official SDK behind one interface; `src/providers/factory.ts` builds bindings. `src/providers/openai.ts` is the Responses API reference implementation.
- CLI streaming presentation lives in `src/chat/chat-runner.ts`, which turns session chunks into stdout writes/logs. Ink rendering (StatusBar, InputPanel, etc.) stays in `src/ui` so UIs can swap without touching providers; `ChatWindow` is currently a test/legacy helper.
- The TUI shell supports slash commands (e.g., `/help`, `/login`, `/provider`, `/model`, `/clear`), multi-turn history, and a first-run config step before chat input. Configuration helpers (env parsing, provider selection) live in `src/config`. Ink components live in `src/ui`; shared hooks (like `useSession`) go in `src/ui/hooks`.
- Runtime shims `bin/run.js` / `bin/dev.js` load the compiled `dist` bundle; treat `dist` as read-only. Mirror this layout under `test/`.

## Build, Test & Development Commands
- `yarn build`: removes `dist` and runs `tsc -b`; run when command signatures change.
- `yarn test`: `TSX_TSCONFIG_PATH=test/tsconfig.json node --import tsx ./node_modules/mocha/bin/mocha --forbid-only "test/**/*.test.{ts,tsx}"` then `yarn lint` via `posttest`. Run `yarn lint` directly to iterate on ESLint issues.
- Dev: run `node bin/dev.js` (tsx dev mode) to launch the TUI; set env like `AICE_PROVIDER=openai DEBUG=* node bin/dev.js` to debug providers.
- Pre-publish: `yarn prepack` refreshes the manifest and README.

## Coding Style & Naming Conventions
- ECMAScript modules, 2-space indentation, explicit exports.
- Command IDs stay kebab-cased (`chat stream` -> `src/commands/chat/stream.ts`).
- Provider classes use PascalCase, accept SDK clients via constructor injection, and expose async generators for chunked output.
- Ink components are PascalCase functions; shared hooks live in `src/ui/hooks`.
- Avoid `any`; prefer concrete interfaces/types. If you must use `unknown`, call it out in your change note/response.
- Keep side effects inside `Command.run()` or focused services so UI and provider logic stay testable.

## Testing Guidelines
- Use Mocha + Chai + `@oclif/test` for commands; place suites next to their modules (e.g., `test/commands/chat.test.ts`).
- Provider tests stub SDK calls (sinon or hand-rolled fakes) and assert chunk ordering plus error propagation.
- Ink components use `ink-testing-library`, ensuring streaming renders incrementally and the status bar mirrors provider + token usage.
- Add regression tests for failure modes (invalid API keys, timeouts, provider mismatches).
- Keep tests fast/reliable; they are the safety net for refactors.

## Provider Configuration & Security
- Load configuration from `.env`; keep secrets out of Git. Supported providers: `openai`, `deepseek`.
- Require matching API keys (`AICE_OPENAI_API_KEY`, `AICE_DEEPSEEK_API_KEY`) plus optional overrides (`AICE_OPENAI_BASE_URL`, `AICE_OPENAI_MODEL`, etc.).
- Validate inputs before instantiating SDK clients and display actionable errors in the status bar. First-run flow prompts for provider + API key, validates connectivity, and writes `.env` with secrets redacted in logs.
- Gate verbose HTTP tracing behind `DEBUG` and redact prompt text in logs. When new providers land, add their keys and wire selection through the provider factory.

## Commit & Pull Request Guidelines
- Commits favor short imperative subjects; add Conventional Commit prefixes only when they clarify scope.
- PRs describe the scenario, list providers exercised, and attach terminal recordings or GIFs whenever the Ink UI changes.
- Always note the dev command you ran, confirm `yarn build && yarn test`, and link the relevant issue or follow-up tasks.

## Planning & Tracking
- Maintain a living `TODO.md` at the repo root. Every major phase (dependencies, session layer, providers, TUI shell/slash commands) should have a checkbox entry, updated as work progresses.
- When plans change (e.g., shifting to the TUI-first `aice` entry), edit both `TODO.md` and this guide so contributors know the current roadmap and documentation expectations.

## Practical Refactor Checklist
- Spot the smell → pick the smallest refactor → run tests.
- Preserve behavior; prefer extracting/moving code over rewriting.
- Stage risky changes behind tests or flags; keep commits reviewable and reversible.
