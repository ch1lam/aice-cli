The ledger CLI in the current directory needs refunds and a threshold. Keep
every existing behavior.

- Transactions gain an optional `kind` column. Without the column every row is
  a `sale`. With it, each value, trimmed, must be `sale` or `refund`; anything
  else, including empty, makes the row invalid. Sales add their amount and
  refunds subtract it.
- Add `--minimum AMOUNT`, default `0`, using the amount syntax. Compute each
  customer's final net first, then print customers whose net is at least the
  threshold; never filter individual transactions. An invalid threshold exits 2.

This touches CSV decoding, the business rules and argument handling; keep each
concern where it belongs. Add tests for the change.
