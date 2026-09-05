#!/usr/bin/env python3
"""Black-box acceptance: python acceptance.py CANDIDATE --stage 1..4."""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def check(candidate, stage):
    passed = []

    def case(name, data, expected, args=(), code=0, diagnostic=None, file_input=False):
        with tempfile.TemporaryDirectory(prefix="ledger-case-") as temp:
            path = Path(temp) / "input.csv"
            path.write_text(data, encoding="utf-8")
            environment = dict(os.environ, PYTHONDONTWRITEBYTECODE="1", PYTHONUTF8="1")
            environment.pop("PYTHONPATH", None)
            result = subprocess.run(
                [sys.executable, "-m", "ledger", str(path) if file_input else "-", *args],
                cwd=candidate, input=data.encode("utf-8"), capture_output=True,
                env=environment, timeout=10,
            )
        assert result.returncode == code, (name, "exit", result.returncode, result.stderr)
        assert result.stdout == expected.encode("utf-8"), (name, "stdout", result.stdout, expected)
        stderr = result.stderr.decode("utf-8")
        if diagnostic is None:
            assert stderr == "", (name, "unexpected stderr", stderr)
        else:
            assert diagnostic in stderr, (name, "diagnostic", stderr)
        passed.append(name)

    case("sum-sort-file", "customer,amount\nZed,10.00\nAlice,2.50\nZed,3.50\n",
         "customer,net\nAlice,2.50\nZed,13.50\n", file_input=True)
    case("csv-quoting", 'customer,amount\n"A, B",1.00\n Alice ,2.00\n',
         'customer,net\n"A, B",1.00\nAlice,2.00\n')
    case("empty-data", "customer,amount\n", "customer,net\n")
    case("invalid-atomic", "customer,amount\nA,10\nB,nope\n", "", code=2, diagnostic="line 3")
    case("skip-invalid", "customer,amount\nA,10\nB,nope\nA,2.50\n",
         "customer,net\nA,12.50\n", args=("--skip-invalid",), diagnostic="line 3")
    case("multiline-physical-line", 'customer,amount\n"A\nB",1.00\nC,nope\n',
         'customer,net\n"A\nB",1.00\n', args=("--skip-invalid",), diagnostic="line 4:")
    for amount in ("-1", "NaN", "Infinity", "1.001", "1e2", "", " ", " 1", "1 ",
                   "1000000000000000000000000"):
        case("reject-amount-" + amount, f"customer,amount\nA,{amount}\n", "", code=2, diagnostic="line 2")
    case("header", "client,amount\nA,1\n", "", code=2, diagnostic="error:")
    case("duplicate-header", "customer,amount,amount\nA,1,2\n", "", code=2, diagnostic="error:")
    case("missing-field", "customer,amount\nA\n", "", code=2, diagnostic="line 2")
    case("extra-field", "customer,amount\nA,1,2\n", "", code=2, diagnostic="line 2")
    case("blank-customer", "customer,amount\n ,1\n", "", code=2, diagnostic="line 2")
    if stage >= 2:
        data = "customer,amount,kind\nA,20,sale\nA,15,refund\nB,4,sale\nB,4,sale\nC,2,refund\n"
        case("net-before-minimum", data, "customer,net\nB,8.00\n", args=("--minimum", "6"))
        case("threshold-inclusive", data, "customer,net\nA,5.00\nB,8.00\n", args=("--minimum", "5"))
        case("unknown-kind", "customer,amount,kind\nA,1,chargeback\n", "", code=2, diagnostic="line 2")
        case("bad-minimum", "customer,amount\nA,1\n", "", args=("--minimum", "-1"), code=2, diagnostic="error:")
    if stage >= 3:
        case("large-exact-cents", "customer,amount,kind\nA,9007199254740992.01,sale\nA,9007199254740992.00,refund\n",
             "customer,net\nA,0.01\n")
        case("decimal-addition", "customer,amount\nA,0.10\nA,0.20\n", "customer,net\nA,0.30\n")
        case("permutation", "customer,amount,kind\nA,9007199254740992.00,refund\nA,9007199254740992.01,sale\n",
             "customer,net\nA,0.01\n")
        case("maximum-amount", "customer,amount\nA,999999999999999999999999.99\n",
             "customer,net\nA,999999999999999999999999.99\n")
    if stage >= 4:
        data = "customer,amount,kind\nAlpha,12,sale\nAlpha,5,refund\nAlpine,8,sale\nBeta,20,sale\nalpha,30,sale\n"
        case("prefix-net-threshold", data, "customer,net\nAlpine,8.00\n",
             args=("--customer-prefix", "Al", "--minimum", "8"))
        case("prefix-empty-result", data, "customer,net\n", args=("--customer-prefix", "missing"))
    return passed


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("--stage", type=int, choices=range(1, 5), required=True)
    options = parser.parse_args()
    names = check(options.candidate.resolve(), options.stage)
    print(f"PASS stage {options.stage}: {len(names)} process cases")
