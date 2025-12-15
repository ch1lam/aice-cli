# TODO

## Completed (MVP + polish)
- [x] Install baseline dependencies (`openai`, `ink`, `react`, `dotenv`, `@types/react`) and re-run `yarn test` / `yarn lint`.
- [x] Define session + streaming interfaces in `src/core/session` and `src/core/stream`; add unit tests for chunk sequencing.
- [x] Implement `src/providers/openai.ts` using the official SDK and expose an async generator for streaming text.
- [x] Add a controller + provider binding layer between the CLI/TUI and providers; keep CLI rendering in `src/chat/chat-runner.ts`.
- [x] Clone the provider pattern for DeepSeek (official SDK) and allow `--provider` switching via the binding factory.
- [x] Make `aice` (no args) launch the Ink TUI with a chat input bar; keep `chat` for scripted single-turn calls.
- [x] Add a first-run setup flow that guides provider selection and API key entry, writes `.env`, and validates connectivity; expose `/login` to update credentials later.
- [x] Implement a slash-command router in the TUI (`/help`, `/provider`, `/model`, `/login`, `/clear`) with status feedback.
- [x] Support multi-turn sessions in the TUI, keeping history and provider settings across messages.
- [x] Provider event semantics: all `providers/*.ts` emit `status:running` at start, `status:completed/failed` at end, emit a single `usage`, and surface underlying error code/message; covered by unit tests.
- [x] Prompt builder: structured `buildPrompt` (role labels, optional truncation, no persistence) with unit tests.
- [x] Config I/O abstraction: injectable I/O for `.env` read/write (testable), centralized required-field validation, avoids global `process.env` pollution; tests cover I/O failures.
- [x] First-run connectivity: after setup writes `.env`, run lightweight provider ping; on failure, stay in setup with actionable error; tests stub the ping.

## Refactor Roadmap (progressive, smallest change first)
1. [x] **Sync docs with current structure**: update this list (done), then align `AGENTS.md`/`README.md` paths and module names (e.g. `src/chat/*`, TUI-only `ChatWindow` status).
2. [x] **Extract UI pure utils**: move `clampIndex`/`cycleIndex`/`cycleProviderChoice` into `src/ui/utils.ts`; update `src/ui/aice-app.tsx`, `src/ui/select-input.tsx`, `src/ui/slash-suggestions.tsx`.
3. [x] **Centralize providerId parsing/guards (no type change yet)**: add `KNOWN_PROVIDERS` + `isProviderId/parseProviderId` in `src/core/stream.ts` or `src/config/env.ts`; replace scattered parsing.
4. [x] **Tighten `ProviderId` typing**: change to closed union (`'openai' | 'deepseek'`); validate unknown env/provider inputs and remove `as ProviderId` casts across UI/core/config.
5. [x] **Centralize provider defaults**: introduce `src/config/provider-defaults.ts` for default model/baseURL/shared fallbacks; replace hard-coded defaults in `src/config/env.ts`, `src/providers/factory.ts`, `src/providers/ping.ts`, `src/ui/provider-options.ts`.
6. [x] **Unify env credential types**: collapse `ProviderEnv`/`ProviderCredentials` duplication in `src/config/env.ts` (single source of truth), keep public API stable via type alias if needed.
7. [x] **Harden OpenAI error fallback**: ensure `OpenAIProvider.#toError` never produces `undefined` messages; add regression tests.
8. [x] **Shared `toError` helper**: add `src/core/errors.ts` and make both providers use it for consistent error formatting.
9. [x] **Single provider mismatch invariant**: remove redundant mismatch check (`ChatController.#assertProvider` or `runSession`), keep one source of truth; adjust tests.
10. [x] **Unify stream consumption**: make `useChatStream` reuse `useSession` (or extract a shared consumer), so UI paths share one chunk reader.
11. [x] **Split `AiceApp` responsibilities**: extract focused hooks (`useKeybindings`, `useSlashSuggestionsState`, `useChatInputController`); `AiceApp` becomes render + wiring only.
12. [ ] **Registry-driven provider binding/ping**: replace switches in `src/providers/factory.ts`/`src/providers/ping.ts` with `providerRegistry` (id → class + defaults + request/ping helpers).
13. [ ] **Shared provider streaming base/helper**: pull common status/usage/abort plumbing out of `OpenAIProvider`/`DeepSeekProvider`; keep provider-specific delta mapping only.
14. [ ] **Decide `src/domain` fate**: either implement real chat domain types and migrate imports gradually, or remove empty domain layer and update docs.
15. [ ] **Add application services layer**: introduce `src/application/chat-service.ts` and `src/application/setup-service.ts`; UI hooks call services, `ChatController` becomes thin facade.
16. [ ] **Interface boundary cleanup**: move oclif/Ink entrypoints into `src/interface/{cli,tui}`; keep core/domain/providers/config free of UI/oclif imports.
17. [ ] **Optional plugin-style extension points**: model providers + slash commands as registries/plugins to allow third-party extensions without core switches.
