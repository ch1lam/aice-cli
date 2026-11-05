Build an in-memory HTTP notes service in Go in the current directory, which
holds only `go.mod` (module `example.test/notes`; keep that path). Use only the
standard library.

Provide package `notes` in directory `notes/` (import path
`example.test/notes/notes`) with `func NewHandler() http.Handler`, and a
runnable `cmd/server`. `go run ./cmd/server` listens at `ADDR`, defaulting to
`127.0.0.1:8080`. Each handler owns independent state and is safe under
simultaneous requests.

- `POST /notes` accepts a JSON object with `text`. Trim surrounding whitespace;
  accept 1–200 Unicode code points, including multibyte characters. Return 201
  and a JSON object with a positive integer `id` and the stored `text`.
- IDs strictly increase per handler and are never reused after deletion.
- `GET /notes` returns 200 and a JSON array ordered by ascending ID, `[]` when
  empty. JSON responses have content type `application/json`.
- `DELETE /notes/{id}` returns 204 for an existing note and 404 for absent,
  invalid or nonpositive IDs. Unknown routes return 404; unsupported methods on
  known routes return 405. Error bodies are not specified.
- Malformed JSON, wrong field types, unknown fields, missing, blank or too long
  text, trailing JSON values and bodies over 4096 bytes return 400 and leave
  stored notes unchanged.

Additional response fields are allowed. No persistence or update endpoint is
required. Add tests for the behavior you build; they must pass with
`go test ./...`.
