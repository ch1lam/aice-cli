# aice-cli

Early-stage CLI for experimenting with cloud LLM providers. It defaults to an Ink TUI when you run `aice` with no args and still exposes a scriptable chat command for single-turn calls.

## Status
- Ready: streaming chat path (`chat` -> controller -> session/stream -> provider) for OpenAI/Anthropic/DeepSeek; provider adapters follow a shared chunking interface.
- Ready: TUI-first default command (`aice`) that opens an Ink chat shell with an input bar, streaming transcript, first-run setup for provider/API key, and slash commands (`/help`, `/login`, `/provider`, `/model`, `/clear`).
- Ready: OpenAI Agents SDK provider wired into the shared streaming path for higher-level orchestration.
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
AICE_MODEL=gpt-4o-mini           # optional OpenAI override

# OpenAI Agents
# AICE_PROVIDER=openai-agents
# AICE_OPENAI_API_KEY=sk-...
# AICE_OPENAI_AGENT_MODEL=gpt-4.1 # optional override for the Agents SDK

# Anthropic
# AICE_PROVIDER=anthropic
# AICE_ANTHROPIC_API_KEY=sk-ant-...
# AICE_ANTHROPIC_MODEL=claude-3-5-sonnet-latest

# DeepSeek
# AICE_PROVIDER=deepseek
# AICE_DEEPSEEK_API_KEY=sk-deep-...
# AICE_DEEPSEEK_BASE_URL=https://api.deepseek.com # optional override (OpenAI-compatible)
# AICE_DEEPSEEK_MODEL=deepseek-chat
```

`AICE_MODEL` only applies to OpenAI. Set `AICE_ANTHROPIC_MODEL` or `AICE_DEEPSEEK_MODEL` for other providers.

## Usage
### TUI
- Run `aice` (or `node bin/dev.js`) with no args to open the Ink UI. On first run it prompts for provider + API key, writes `.env`, and validates connectivity. Slash commands (prefixed with `/`) handle help, login, provider/model switching, and clearing the transcript; plain input sends messages.

### Single-turn CLI
- Run the TypeScript CLI directly via the dev entrypoint:
  ```bash
  AICE_OPENAI_API_KEY=sk-test node bin/dev.js chat "Hello there"
  # or pick another provider
  AICE_ANTHROPIC_API_KEY=sk-ant-test node bin/dev.js chat -p anthropic "Hi Claude"
  ```
- The MVP shell streams chunks to stdout: `chat.ts` parses flags, `ChatController` delegates to the provider via the session layer, and `chat-runner.ts` renders meta/status/text/usage chunks.

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
