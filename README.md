# aice-cli

DeepSeek-only CLI for experimenting with the Vercel AI SDK. It defaults to an Ink TUI when you run `aice` with no args.

## Status
- DeepSeek-only, no provider picker.
- TUI-first default command (`aice`) opens an Ink chat shell with an input bar, streaming transcript, and first-run setup for API key/model.
- Slash commands: `/help`, `/login`, `/model <name>`, `/new`.

## Requirements
- Node.js >= 18
- Yarn 1.x (or npm/pnpm equivalents)

Install dependencies:

```bash
yarn install
```

Create a `.env` (or export vars) for the DeepSeek provider (Vercel AI SDK):

```
# DeepSeek (Vercel AI SDK)
DEEPSEEK_API_KEY=sk-deep-...
DEEPSEEK_BASE_URL=https://api.deepseek.com # optional override
DEEPSEEK_MODEL=deepseek-chat               # optional override
```

These names mirror AI SDK conventions. Legacy `AICE_*` provider keys are ignored and removed when `.env` is persisted.

## Usage
- Run `aice` (or `node bin/dev.js`) with no args to open the Ink UI (requires a real TTY). On first run it prompts for API key/model, writes `.env`, and validates connectivity. Slash commands (prefixed with `/`) handle help, login, model switching, and starting a new session; plain input sends messages.

## Project structure
- `bin/` — runtime shims (`run.js`/`dev.js`); `aice` defaults to `tui` when no args are provided.
- `src/commands/` — oclif commands (currently `tui`).
- `src/ui/` — Ink TUI (components + hooks). Entry point is `src/ui/run-tui.ts`.
- `src/services/` — services that coordinate config + providers (`ChatService`, `SetupService`).
- `src/providers/` — provider adapters + registry + connectivity ping.
- `src/core/` — session runner (adds meta chunk, validates provider id) and shared error formatting.
- `src/types/` — shared types (chat messages, stream chunks) with no side effects.
- `src/config/` — `.env` load/persist and provider defaults.
- `src/chat/` — prompt building from chat history.
- `test/` — mirrors `src/` using Mocha + Chai.

Dependency rule of thumb: UI/commands → services → (config/providers/core/types). Keep `core`/`types` free of Ink/oclif imports.

## Development
- `yarn build` — type-check and compile to `dist/`
- `yarn test` — run the Mocha suite (lint runs via `posttest`)
- `yarn lint` — ESLint with the oclif + Prettier config
- `node bin/dev.js --help` — run the CLI in tsx dev mode

## Contributing
1. Create a feature branch and implement the module (core session, provider, or Ink UI component).
2. Add matching tests under `test/`.
3. Run `yarn build && yarn test`.
4. Open a PR describing the behavior, provider coverage, and attach a short terminal recording if the TUI changes.

See `AGENTS.md` for deeper architectural guidelines and directory responsibilities.
