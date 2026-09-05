#!/usr/bin/env python3
"""Generate stage starters and self-check the corpus without model calls."""
import argparse
from pathlib import Path
import shutil
import tempfile

from acceptance import check

ROOT = Path(__file__).resolve().parent


def generate(target, stage):
    target = target.resolve()
    if target.exists():
        raise ValueError("target must not exist")
    target.mkdir(parents=True)
    if stage == 1:
        return
    shutil.copytree(ROOT / "reference" / "ledger", target / "ledger",
                    ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    cli = target / "ledger" / "cli.py"
    cli.write_text(cli.read_text().replace("FEATURE_STAGE = 4", f"FEATURE_STAGE = {stage - 1}"))
    if stage == 3:
        domain = target / "ledger" / "domain.py"
        domain.write_text(domain.read_text().replace(
            "return coefficient * 10 ** (digits.exponent + 2)",
            "return int(round(float(value) * 100))  # seeded precision defect"))
    if stage == 4:
        # A working but tangled single-file baseline: preserve behavior while
        # assigning parsing, business rules, and CLI responsibilities clearly.
        sources = []
        for name in ("domain.py", "csv_io.py", "cli.py"):
            path = target / "ledger" / name
            sources.append("\n".join(line for line in path.read_text().splitlines()
                                     if not line.startswith("from .")))
            path.unlink()
        (target / "ledger" / "cli.py").write_text("\n\n".join(sources) + "\n")


def self_check():
    for stage in range(1, 5):
        names = check(ROOT / "reference", stage)
        print(f"reference stage {stage}: PASS ({len(names)} cases)")
    with tempfile.TemporaryDirectory(prefix="ledger-selfcheck-") as temp:
        for stage in range(2, 5):
            target = Path(temp) / str(stage)
            generate(target, stage)
            names = check(target, stage - 1)
            print(f"starter stage {stage}: prior stage PASS ({len(names)} cases)")
            try:
                check(target, stage)
            except AssertionError as exc:
                failure = exc.args[0][0]
                expected = {2: "net-before-minimum", 3: "large-exact-cents", 4: "prefix-net-threshold"}[stage]
                if failure != expected:
                    raise AssertionError(("unexpected starter failure", stage, failure, expected)) from exc
                print(f"starter stage {stage}: expected failure {failure}")
            else:
                raise AssertionError(f"starter stage {stage} unexpectedly passes")
        crlf = Path(temp) / "crlf-output"
        shutil.copytree(ROOT / "reference", crlf,
                        ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        csv_io = crlf / "ledger" / "csv_io.py"
        csv_io.write_text(csv_io.read_text().replace('lineterminator="\\n"', 'lineterminator="\\r\\n"'))
        try:
            check(crlf, 1)
        except AssertionError as exc:
            if exc.args[0][0:2] != ("sum-sort-file", "stdout"):
                raise
            print("CRLF mutation: expected byte-output failure sum-sort-file")
        else:
            raise AssertionError("CRLF output unexpectedly passes")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("self-check")
    start = sub.add_parser("start")
    start.add_argument("stage", type=int, choices=range(1, 5))
    start.add_argument("target", type=Path)
    args = parser.parse_args()
    if args.command == "self-check":
        self_check()
    else:
        generate(args.target, args.stage)
