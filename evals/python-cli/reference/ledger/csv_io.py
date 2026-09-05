"""CSV decoding and deterministic output."""
import csv
from .domain import transaction, format_money


def read_transactions(source, skip_invalid, errors):
    reader = csv.DictReader(source)
    if not reader.fieldnames or not {"customer", "amount"} <= set(reader.fieldnames):
        raise ValueError("required columns: customer, amount")
    if len(set(reader.fieldnames)) != len(reader.fieldnames):
        raise ValueError("duplicate columns")
    rows = []
    for row in reader:
        try:
            if None in row or any(value is None for value in row.values()):
                raise ValueError("wrong number of columns")
            rows.append(transaction(row))
        except ValueError as exc:
            if not skip_invalid:
                raise ValueError(f"line {reader.line_num}: {exc}") from exc
            errors.write(f"line {reader.line_num}: skipped: {exc}\n")
    return rows


def write_summary(rows, output):
    writer = csv.writer(output, lineterminator="\n")
    writer.writerow(["customer", "net"])
    for customer, total in rows:
        writer.writerow([customer, format_money(total)])
