# TODO

- [x] Install baseline dependencies (`openai`, `ink`, `react`, `dotenv`, `@types/react`) and re-run `yarn test` / `yarn lint`.
- [x] Define session + streaming interfaces in `src/core/session` and `src/core/stream`; add unit tests for chunk sequencing.
- [x] Implement `src/providers/openai.ts` using the official SDK and expose an async generator for streaming text.
- [x] Build `src/commands/chat.ts` plus config loading (`dotenv`) so prompts can be sent via CLI flags and streamed to stdout.
- [x] Add a controller + provider binding layer between the CLI and providers; keep CLI rendering in `chat-runner.ts`.
- [x] Create Ink components (`ChatWindow`, `StatusBar`) that render streaming output and provider status; cover with `ink-testing-library`.
- [x] Clone the provider pattern for Anthropic and DeepSeek (official SDKs) and allow `--provider` switching via the binding factory.
- [x] Make `aice` (no args) launch the Ink TUI with a chat input bar; keep `chat` for scripted single-turn calls.
- [x] Add a first-run setup flow that guides provider selection and API key entry, writes `.env`, and validates connectivity; expose `/login` to update credentials later.
- [x] Implement a slash-command router in the TUI (`/help`, `/provider`, `/model`, `/login`, `/clear` to start) with status feedback.
- [x] Support multi-turn sessions in the TUI, keeping history and provider settings across messages.
- [x] Layer in the OpenAI Agents SDK for high-level orchestration once the core streaming path and TUI shell are stable.
