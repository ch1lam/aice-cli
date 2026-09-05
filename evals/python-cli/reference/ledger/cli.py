"""CLI ownership: arguments, streams, and exit status."""
import argparse
import csv
import sys
from .domain import money, summarize
from .csv_io import read_transactions, write_summary

FEATURE_STAGE = 4


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("input", help="UTF-8 CSV path, or - for stdin")
    parser.add_argument("--skip-invalid", action="store_true")
    if FEATURE_STAGE >= 2:
        parser.add_argument("--minimum", default="0")
    if FEATURE_STAGE >= 4:
        parser.add_argument("--customer-prefix", default="")
    args = parser.parse_args()
    try:
        minimum = money(getattr(args, "minimum", "0"))
        if args.input == "-":
            rows = read_transactions(sys.stdin, args.skip_invalid, sys.stderr)
        else:
            with open(args.input, encoding="utf-8", newline="") as source:
                rows = read_transactions(source, args.skip_invalid, sys.stderr)
        write_summary(summarize(rows, minimum, getattr(args, "customer_prefix", "")), sys.stdout)
    except (OSError, ValueError, csv.Error, UnicodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    return 0
