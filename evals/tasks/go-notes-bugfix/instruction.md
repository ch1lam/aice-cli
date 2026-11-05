The current directory holds an in-memory Go HTTP notes service (package
`example.test/notes/notes`, `NewHandler() http.Handler`). Notes have a `text`
of 1–200 Unicode code points after trimming and an optional lowercase `tag`,
and `GET /notes` supports `tag` and `limit` filters.

Bug report: "A note containing 200 `界` characters is rejected even though the
documented limit is 200 characters. The same-length ASCII note succeeds."

Reproduce the issue, fix its cause and add a regression test. Keep rejecting
201 code points and keep every other behavior. Do not change the requirement to
bytes or raise a numeric limit until the examples happen to fit. Tests must
pass with `go test ./...`.
