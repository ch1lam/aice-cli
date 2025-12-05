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

## Next Up (no new persistence except config)
- [x] TUI failure visibility: on stream `error`, set `sessionStatus=failed` and show error state/message in `StatusBar`; add Ink tests for success/failure cases.
- [x] First-run connectivity: after setup writes `.env`, run a lightweight provider ping (minimal per provider). On failure, stay in setup with actionable error; add stubbed tests for the ping.
- [ ] Setup overrides: collect optional baseURL/model/Agents instructions during setup; pass to `persistProviderEnv` with no extra persistence. Ensure provider switching keeps existing overrides.
- [ ] Provider event semantics: all `providers/*.ts` should emit `status:running` at start, `status:completed/failed` at end, emit a single `usage`, and surface underlying error code/message. Add unit tests covering the differences.
- [ ] Slash command routing table: extract command definitions (name/usage/handler), centralize parse/validation, keep Tab/up/down behavior; add tests for unknown/empty commands.
- [ ] Split TUI logic: extract `useSetupFlow` (provider select/save), `useChatStream` (consume SessionStream), `useSlashCommands` (table-driven). `AiceApp` should only render and wire state.
- [ ] Prompt builder: add structured `buildPrompt` (role labels, optional truncation, no persistence) and replace the `startStream` string concat; add unit test.
- [ ] Config I/O abstraction: provide injectable I/O for `.env` read/write (testable), centralize required-field validation, avoid global `process.env` pollution; add tests for I/O failures.
