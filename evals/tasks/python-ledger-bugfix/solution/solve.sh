#!/bin/bash
# Convert amounts to integer cents exactly instead of through a float.
set -euo pipefail
python3 - <<'PY'
from pathlib import Path
path = Path("/app/ledger/domain.py")
source = path.read_text()
old = "    return int(round(float(amount) * 100))\n"
new = (
    "    digits = amount.as_tuple()\n"
    '    coefficient = int("".join(map(str, digits.digits)))\n'
    "    return coefficient * 10 ** (digits.exponent + 2)\n"
)
assert source.count(old) == 1
path.write_text(source.replace(old, new))
PY
