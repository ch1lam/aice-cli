# aice-cli

Early-stage CLI for experimenting with cloud LLM providers. The current repo is an oclif shell with a streaming chat path and a provider layer (OpenAI/Anthropic/DeepSeek), plus room for an Ink-based TUI.

## Status
- Ready: oclif skeleton + streaming chat path (`chat` -> controller -> session/stream -> provider) for OpenAI/Anthropic/DeepSeek
- In progress: Ink UI components and higher-level orchestration
- On deck: prompt history, provider/agent orchestration, and extensibility hooks

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

# Anthropic
# AICE_PROVIDER=anthropic
# AICE_ANTHROPIC_API_KEY=sk-ant-...
# AICE_ANTHROPIC_MODEL=claude-3-5-sonnet-latest

# DeepSeek
# AICE_PROVIDER=deepseek
# AICE_DEEPSEEK_API_KEY=sk-deep-...
# AICE_DEEPSEEK_MODEL=deepseek-chat
```

`AICE_MODEL` only applies to OpenAI. Set `AICE_ANTHROPIC_MODEL` or `AICE_DEEPSEEK_MODEL` for other providers.

## Usage
Run the TypeScript CLI directly via the dev entrypoint:

```bash
AICE_OPENAI_API_KEY=sk-test node bin/dev.js chat "Hello there"
# or pick another provider
AICE_ANTHROPIC_API_KEY=sk-ant-test node bin/dev.js chat -p anthropic "Hi Claude"
```

The MVP shell streams chunks to stdout: `chat.ts` parses flags, `ChatController` delegates to the provider via the session layer, and `chat-runner.ts` renders meta/status/text/usage chunks.

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
