# TODO

- [x] Install baseline dependencies (`openai`, `ink`, `react`, `dotenv`, `@types/react`) and re-run `yarn test` / `yarn lint`.
- [x] Define session + streaming interfaces in `src/core/session` and `src/core/stream`; add unit tests for chunk sequencing.
- [x] Implement `src/providers/openai.ts` using the official SDK and expose an async generator for streaming text.
- [x] Build `src/commands/chat.ts` plus config loading (`dotenv`) so prompts can be sent via CLI flags and streamed to stdout.
- [x] Add a controller + provider binding layer between the CLI and providers; keep CLI rendering in `chat-runner.ts`.
- [x] Create Ink components (`ChatWindow`, `StatusBar`) that render streaming output and provider status; cover with `ink-testing-library`.
- [x] Clone the provider pattern for Anthropic and DeepSeek (official SDKs) and allow `--provider` switching via the binding factory.
- [ ] Layer in the OpenAI Agents SDK for high-level orchestration once the core streaming path is stable.
