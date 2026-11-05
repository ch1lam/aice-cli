Add a customer filter to the ledger CLI in the current directory. Keep every
existing behavior.

`--customer-prefix TEXT` keeps customers whose trimmed name starts with TEXT,
compared literally and case-sensitively; the default empty prefix matches every
customer. It applies together with `--minimum` and does not change how nets are
computed.

Add tests for the change.
