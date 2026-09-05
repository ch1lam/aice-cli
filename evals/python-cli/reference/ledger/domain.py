"""Exact monetary rules, independent of CSV and command-line handling."""
from decimal import Decimal, InvalidOperation
import re


def money(value):
    if not re.fullmatch(r"[0-9]{1,24}(?:\.[0-9]{1,2})?", value):
        raise ValueError("invalid amount")
    try:
        amount = Decimal(value)
    except InvalidOperation as exc:
        raise ValueError("invalid amount") from exc
    if not amount.is_finite() or amount < 0 or amount.as_tuple().exponent < -2:
        raise ValueError("amount must be nonnegative with at most two decimal places")
    # Integer cents avoid Decimal context precision limits for large totals.
    digits = amount.as_tuple()
    coefficient = int("".join(map(str, digits.digits)))
    return coefficient * 10 ** (digits.exponent + 2)


def transaction(row):
    customer = row["customer"].strip()
    if not customer:
        raise ValueError("empty customer")
    amount = money(row["amount"])
    kind = row.get("kind", "sale").strip()
    if kind not in ("sale", "refund"):
        raise ValueError("invalid kind")
    return customer, -amount if kind == "refund" else amount


def summarize(transactions, minimum=0, prefix=""):
    totals = {}
    for customer, amount in transactions:
        totals[customer] = totals.get(customer, 0) + amount
    return [(name, total) for name, total in sorted(totals.items())
            if total >= minimum and name.startswith(prefix)]


def format_money(cents):
    sign = "-" if cents < 0 else ""
    whole, fraction = divmod(abs(cents), 100)
    return f"{sign}{whole}.{fraction:02d}"
