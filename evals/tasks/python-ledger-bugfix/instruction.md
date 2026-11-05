The current directory holds a Python CSV ledger CLI, run as
`python -m ledger INPUT [--skip-invalid] [--minimum AMOUNT] [--customer-prefix TEXT]`.
It sums each customer's sales minus refunds and prints `customer,net` with
exactly two decimal places. Amounts have 1–24 integer digits and up to two
decimal places, and every calculation must be exact to the cent.

Bug report: customer A has a sale of `9007199254740992.01` and a refund of
`9007199254740992.00`. The result should be `0.01`, but the CLI prints `0.00`.

Find where precision is lost and fix it without narrowing the documented
amount range, and keep the command compatible. Add a regression test with the
standard library `unittest`; `python -m unittest discover` from this directory
must run it.
