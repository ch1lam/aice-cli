Build a command-line ledger in Python in the current directory, which is
empty. Use only the standard library. It runs as
`python -m ledger INPUT [--skip-invalid]` from this directory and sums each
customer's amounts.

- INPUT is a UTF-8 CSV file, or `-` for standard input. Required columns are
  `customer,amount` in any order; other named columns are ignored. Duplicate
  column names or a missing required column are errors. Quoting follows the
  Python `csv` module.
- A customer is trimmed of surrounding whitespace, must not be empty and is
  case-sensitive. An amount is 1–24 ASCII digits with an optional one or two
  decimal places: no sign, exponent, whitespace or non-finite values. Every
  calculation and output is exact to the cent.
- Output is CSV with header `customer,net`, sorted by customer in Unicode code
  point order, amounts with exactly two decimal places and LF line endings.
  Only customers whose net is at least 0 are printed. Empty data prints only
  the header.
- Success exits 0. Input, argument or file errors exit 2 with a diagnostic on
  stderr and nothing on stdout. By default an invalid row stops the command.
  With `--skip-invalid` invalid rows are skipped: each gets a stderr line with
  the physical line number reported by the CSV reader, and the command exits 0.
  Missing or extra fields, an empty customer and an invalid amount make a row
  invalid. Header errors are never skipped.
- A diagnostic that stops the command starts with `error: `. A diagnostic
  about a row includes `line N: ` with that row's physical line number.

Add tests with the standard library `unittest`; `python -m unittest discover`
from this directory must run them.
