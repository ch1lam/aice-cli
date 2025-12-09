# aice-cli

Early-stage CLI for experimenting with cloud LLM providers. It defaults to an Ink TUI when you run `aice` with no args.

## Status
- Ready: TUI-first default command (`aice`) that opens an Ink chat shell with an input bar, streaming transcript, first-run setup for provider/API key, and slash commands (`/help`, `/login`, `/provider`, `/model`, `/clear`).
- Ready: streaming provider adapters (OpenAI/DeepSeek) behind a shared chunking interface.
- On deck: richer prompt history and provider orchestration polish.

## Requirements
- Node.js >= 18
- Yarn 1.x (or npm/pnpm equivalents)

Install dependencies:

```bash
yarn install
```

Create a `.env` (or export vars) with at least one provider configured:

```
# OpenAI
AICE_PROVIDER=openai
AICE_OPENAI_API_KEY=sk-...
AICE_OPENAI_BASE_URL=https://api.openai.com/v1 # optional override
AICE_MODEL=gpt-4o-mini                 # optional OpenAI override

# DeepSeek
# AICE_PROVIDER=deepseek
# AICE_DEEPSEEK_API_KEY=sk-deep-...
# AICE_DEEPSEEK_BASE_URL=https://api.deepseek.com # optional override (OpenAI-compatible)
# AICE_DEEPSEEK_MODEL=deepseek-chat
```

`AICE_MODEL` is the shared fallback for OpenAI if a provider-specific model is not set. Set `AICE_DEEPSEEK_MODEL` for other providers.

## Usage
- Run `aice` (or `node bin/dev.js`) with no args to open the Ink UI. On first run it prompts for provider + API key, writes `.env`, and validates connectivity. Slash commands (prefixed with `/`) handle help, login, provider/model switching, and clearing the transcript; plain input sends messages.

## Development
- `yarn build` — type-check and compile to `dist/`
- `yarn test` — run the Mocha suite (session/provider/chat-runner tests + smoke test)
- `yarn lint` — ESLint with the oclif + Prettier config
- `node bin/dev.js --help` — run the CLI in ts-node dev mode

## Contributing
1. Create a feature branch and implement the module (core session, provider, or Ink UI component).
2. Add matching tests under `test/`.
3. Run `yarn build && yarn test`.
4. Open a PR describing the behavior, provider coverage, and attach a short terminal recording if the TUI changes.

See `AGENTS.md` for deeper architectural guidelines and the planned directory topology.
