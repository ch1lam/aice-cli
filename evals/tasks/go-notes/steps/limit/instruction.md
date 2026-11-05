Add a limit to listing in the notes service in the current directory. Keep
every existing behavior.

`GET /notes?limit=N` takes one integer from 1 to 100. A missing limit returns
all results. An empty, repeated, non-integer, out-of-range or overflowing value
returns 400. Apply the limit after tag filtering and ID ordering, never change
the stored notes, and support combining `tag` and `limit`.

Add tests for the change.
