#!/usr/bin/env python3
"""Score the ledger CLI in /app after one step: python3 verify.py TASK STEP.

Writes /logs/verifier/reward.json with
  reward         1 when every acceptance case up to this step passes
  acceptance     fraction of those cases passing
  earlier        fraction of earlier steps' cases passing (1 when there are none)
  own_tests      1 when the workspace has unittest tests and they pass
  checks         1 when every Python file compiles
  changed_lines  lines added plus removed since the step started
Cases run `python -m ledger` in /app as a black box with a 10 second limit.
This file is shared verbatim by the python-ledger and python-ledger-bugfix tasks.
"""

import difflib
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

WORKSPACE = Path("/app")
BEFORE = Path("/eval/before")
REWARD = Path("/logs/verifier/reward.json")
IGNORED = {".git", "__pycache__", "setup.sh"}

# Each case: name -> (stdin and file data, expected stdout, arguments, exit code, stderr fragment, read from file).
BASE = {
    "sum-sort-file": ("customer,amount\nZed,10.00\nAlice,2.50\nZed,3.50\n", "customer,net\nAlice,2.50\nZed,13.50\n", (), 0, None, True),
    "csv-quoting": ('customer,amount\n"A, B",1.00\n Alice ,2.00\n', 'customer,net\n"A, B",1.00\nAlice,2.00\n', (), 0, None, False),
    "empty-data": ("customer,amount\n", "customer,net\n", (), 0, None, False),
    "invalid-atomic": ("customer,amount\nA,10\nB,nope\n", "", (), 2, "line 3", False),
    "skip-invalid": ("customer,amount\nA,10\nB,nope\nA,2.50\n", "customer,net\nA,12.50\n", ("--skip-invalid",), 0, "line 3", False),
    "multiline-physical-line": ('customer,amount\n"A\nB",1.00\nC,nope\n', 'customer,net\n"A\nB",1.00\n', ("--skip-invalid",), 0, "line 4:", False),
    **{
        "reject-amount-" + amount: (f"customer,amount\nA,{amount}\n", "", (), 2, "line 2", False)
        for amount in ("-1", "NaN", "Infinity", "1.001", "1e2", "", " ", " 1", "1 ", "1000000000000000000000000")
    },
    "header": ("client,amount\nA,1\n", "", (), 2, "error:", False),
    "duplicate-header": ("customer,amount,amount\nA,1,2\n", "", (), 2, "error:", False),
    "missing-field": ("customer,amount\nA\n", "", (), 2, "line 2", False),
    "extra-field": ("customer,amount\nA,1,2\n", "", (), 2, "line 2", False),
    "blank-customer": ("customer,amount\n ,1\n", "", (), 2, "line 2", False),
}
REFUNDS = "customer,amount,kind\nA,20,sale\nA,15,refund\nB,4,sale\nB,4,sale\nC,2,refund\n"
MINIMUM = {
    "net-before-minimum": (REFUNDS, "customer,net\nB,8.00\n", ("--minimum", "6"), 0, None, False),
    "threshold-inclusive": (REFUNDS, "customer,net\nA,5.00\nB,8.00\n", ("--minimum", "5"), 0, None, False),
    "unknown-kind": ("customer,amount,kind\nA,1,chargeback\n", "", (), 2, "line 2", False),
    "bad-minimum": ("customer,amount\nA,1\n", "", ("--minimum", "-1"), 2, "error:", False),
}
# Exact cents are part of the contract from the first step; refunds arrive later.
EXACT = {
    "decimal-addition": ("customer,amount\nA,0.10\nA,0.20\n", "customer,net\nA,0.30\n", (), 0, None, False),
    "maximum-amount": ("customer,amount\nA,999999999999999999999999.99\n", "customer,net\nA,999999999999999999999999.99\n", (), 0, None, False),
}
EXACT_REFUNDS = {
    "large-exact-cents": ("customer,amount,kind\nA,9007199254740992.01,sale\nA,9007199254740992.00,refund\n", "customer,net\nA,0.01\n", (), 0, None, False),
    "permutation": ("customer,amount,kind\nA,9007199254740992.00,refund\nA,9007199254740992.01,sale\n", "customer,net\nA,0.01\n", (), 0, None, False),
}
PREFIXES = "customer,amount,kind\nAlpha,12,sale\nAlpha,5,refund\nAlpine,8,sale\nBeta,20,sale\nalpha,30,sale\n"
PREFIX = {
    "prefix-net-threshold": (PREFIXES, "customer,net\nAlpine,8.00\n", ("--customer-prefix", "Al", "--minimum", "8"), 0, None, False),
    "prefix-empty-result": (PREFIXES, "customer,net\n", ("--customer-prefix", "missing"), 0, None, False),
}
# Cases each step adds, in step order.
CHAINS = {
    "python-ledger": [("create", BASE | EXACT), ("minimum", MINIMUM | EXACT_REFUNDS), ("refactor", {}), ("prefix", PREFIX)],
    "python-ledger-bugfix": [("bugfix", EXACT | EXACT_REFUNDS)],
}
# The bug-fix seed already implements every feature except the defect.
SEED_CASES = {"python-ledger-bugfix": BASE | MINIMUM | PREFIX}


def cases_for(task: str, step: str) -> tuple[dict, dict]:
    earlier = dict(SEED_CASES.get(task, {}))
    for name, added in CHAINS[task]:
        if name == step:
            return earlier, added
        earlier |= added
    raise SystemExit(f"unknown step {step!r} for {task}")


def passes(name: str, case: tuple) -> bool:
    data, expected, args, code, diagnostic, file_input = case
    with tempfile.TemporaryDirectory(prefix="ledger-case-") as temp:
        path = Path(temp) / "input.csv"
        path.write_text(data, encoding="utf-8")
        environment = dict(os.environ, PYTHONDONTWRITEBYTECODE="1", PYTHONUTF8="1")
        environment.pop("PYTHONPATH", None)
        try:
            result = subprocess.run(
                [sys.executable, "-m", "ledger", str(path) if file_input else "-", *args],
                cwd=WORKSPACE, input=data.encode("utf-8"), capture_output=True, env=environment, timeout=10,
            )
        except subprocess.TimeoutExpired:
            print(f"FAIL {name}: timed out")
            return False
    stderr = result.stderr.decode("utf-8", "replace")
    problems = []
    if result.returncode != code:
        problems.append(f"exit {result.returncode}, want {code}")
    if result.stdout != expected.encode("utf-8"):
        problems.append(f"stdout {result.stdout!r}, want {expected!r}")
    if diagnostic is None and stderr:
        problems.append(f"unexpected stderr {stderr!r}")
    if diagnostic is not None and diagnostic not in stderr:
        problems.append(f"stderr {stderr!r} lacks {diagnostic!r}")
    print(("FAIL " if problems else "PASS ") + name + (": " + "; ".join(problems) if problems else ""))
    return not problems


def own_tests() -> float:
    environment = dict(os.environ, PYTHONDONTWRITEBYTECODE="1")
    try:
        result = subprocess.run(
            [sys.executable, "-m", "unittest", "discover", "-s", ".", "-t", "."],
            cwd=WORKSPACE, capture_output=True, text=True, env=environment, timeout=300,
        )
    except subprocess.TimeoutExpired:
        return 0.0
    ran = re.search(r"^Ran (\d+) tests?", result.stderr, re.MULTILINE)
    print(result.stderr[-2000:])
    return float(result.returncode == 0 and ran is not None and int(ran.group(1)) > 0)


def checks() -> float:
    result = subprocess.run(
        [sys.executable, "-m", "compileall", "-q", "-x", r"(^|/)\.", "."],
        cwd=WORKSPACE, capture_output=True, text=True, env=dict(os.environ, PYTHONPYCACHEPREFIX=tempfile.mkdtemp()),
    )
    print(result.stdout, result.stderr)
    return float(result.returncode == 0)


def files(root: Path) -> dict[str, list[str]]:
    contents = {}
    for path in root.rglob("*"):
        relative = path.relative_to(root)
        if path.is_file() and not IGNORED & set(relative.parts):
            try:
                contents[relative.as_posix()] = path.read_text().splitlines()
            except (UnicodeDecodeError, OSError):
                contents[relative.as_posix()] = [f"<binary {path.stat().st_size}>"]
    return contents


def changed_lines() -> int:
    before, after = files(BEFORE), files(WORKSPACE)
    total = 0
    for name in before.keys() | after.keys():
        for line in difflib.unified_diff(before.get(name, []), after.get(name, []), lineterm="", n=0):
            if line[:1] in "+-" and not line.startswith(("+++", "---")):
                total += 1
    return total


def main(task: str, step: str) -> None:
    earlier, added = cases_for(task, step)
    results = {name: passes(name, case) for name, case in (earlier | added).items()}

    def rate(names) -> float:
        names = list(names)
        return sum(results[n] for n in names) / len(names) if names else 1.0

    rewards = {
        "reward": float(all(results.values())),
        "acceptance": rate(results),
        "earlier": rate(earlier),
        "own_tests": own_tests(),
        "checks": checks(),
        "changed_lines": changed_lines(),
    }
    print(json.dumps(rewards, indent=2))
    REWARD.parent.mkdir(parents=True, exist_ok=True)
    REWARD.write_text(json.dumps(rewards))


if __name__ == "__main__":
    main(*sys.argv[1:3])
