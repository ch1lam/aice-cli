The notes service in the current directory needs tags. Keep every existing
behavior.

- `POST /notes` accepts an optional `tag`, and returned notes include it. An
  omitted or empty tag is allowed. A nonempty tag contains only lowercase ASCII
  letters and has at most 20 characters. An invalid tag returns 400 and stores
  nothing.
- `GET /notes?tag=work` lists notes whose tag equals the filter exactly; an
  empty or omitted filter lists all notes. An invalid filter returns 400.
  Results stay in ascending ID order, and no match returns `[]`.

Add tests for the change. Keep HTTP input handling, storage and querying
coherent rather than bolting the tag onto one layer.
