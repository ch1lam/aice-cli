#!/usr/bin/env python3
"""Score the notes service in /app after one step: python3 verify.py TASK STEP.

Writes /logs/verifier/reward.json with
  reward         1 when every acceptance test up to this step passes
  acceptance     fraction of those tests passing
  earlier        fraction of earlier steps' tests passing (1 when there are none)
  own_tests      1 when the workspace has its own tests and they pass
  checks         1 when gofmt and go vet report nothing
  changed_lines  lines added plus removed since the step started
The acceptance tests run on a copy of /app; the workspace is never changed.
This file is shared verbatim by the go-notes and go-notes-bugfix tasks.
"""

import difflib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

TESTS = Path(__file__).resolve().parent
WORKSPACE = Path("/app")
BEFORE = Path("/eval/before")
REWARD = Path("/logs/verifier/reward.json")
BASE = ["TestBaseLifecycle", "TestBaseRejectedInputHasNoEffect", "TestBaseConcurrentRequests", "TestUnicodeBoundary"]
# Tests each step adds, in step order; a task's steps are a prefix-closed chain.
CHAINS = {
    "go-notes": [("create", BASE), ("tags", ["TestTags"]), ("refactor", []), ("limit", ["TestLimit"])],
    "go-notes-bugfix": [("bugfix", ["TestUnicodeBoundary"])],
}
# The bug-fix seed already implements every feature except the defect.
SEED_TESTS = {"go-notes-bugfix": [t for t in BASE if t != "TestUnicodeBoundary"] + ["TestTags", "TestLimit"]}
IGNORED = {".git", "aice_acceptance", "setup.sh"}
ENV = dict(os.environ, GOWORK="off", GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local", GOFLAGS="-mod=mod")


def tests_for(task: str, step: str) -> tuple[list[str], list[str]]:
    earlier = list(SEED_TESTS.get(task, []))
    for name, added in CHAINS[task]:
        if name == step:
            return earlier, added
        earlier += added
    raise SystemExit(f"unknown step {step!r} for {task}")


def run(command: list[str], cwd: Path, timeout: int = 300) -> subprocess.CompletedProcess:
    try:
        return subprocess.run(command, cwd=cwd, env=ENV, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        return subprocess.CompletedProcess(command, 1, exc.stdout or "", f"timed out after {timeout}s")


def acceptance(names: list[str]) -> dict[str, bool]:
    if not names:
        return {}
    with tempfile.TemporaryDirectory(prefix="notes-acceptance-") as tmp:
        candidate = Path(tmp) / "candidate"
        shutil.copytree(WORKSPACE, candidate, ignore=shutil.ignore_patterns(".git"))
        tests = candidate / "aice_acceptance"
        # The workspace may not substitute evaluator-owned tests.
        shutil.rmtree(tests, ignore_errors=True)
        tests.mkdir()
        shutil.copyfile(TESTS / "service_test.go.txt", tests / "service_test.go")
        pattern = "^(" + "|".join(names) + ")$"
        result = run(["go", "test", "-race", "-count=1", "-timeout=60s", "-json", "-run", pattern, "./aice_acceptance"], candidate)
    passed, output = set(), []
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            output.append(line + "\n")
            continue
        output.append(event.get("Output", ""))
        if event.get("Action") == "pass" and "/" not in event.get("Test", "/"):
            passed.add(event["Test"])
    print("".join(output)[-8000:], result.stderr[-4000:], sep="\n")
    return {name: name in passed for name in names}


def own_tests() -> float:
    listed = run(["go", "list", "-f", "{{len .TestGoFiles}} {{len .XTestGoFiles}}", "./..."], WORKSPACE)
    if listed.returncode != 0 or not any(sum(map(int, line.split())) for line in listed.stdout.splitlines()):
        return 0.0
    return float(run(["go", "test", "-count=1", "./..."], WORKSPACE).returncode == 0)


def checks() -> float:
    unformatted = run(["gofmt", "-l", "."], WORKSPACE)
    vet = run(["go", "vet", "./..."], WORKSPACE)
    print("gofmt:", unformatted.stdout, unformatted.stderr, "vet:", vet.stdout, vet.stderr, sep="\n")
    return float(unformatted.returncode == 0 and not unformatted.stdout.strip() and vet.returncode == 0)


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
    earlier, added = tests_for(task, step)
    results = acceptance(earlier + added)

    def rate(names: list[str]) -> float:
        return sum(results[n] for n in names) / len(names) if names else 1.0

    rewards = {
        "reward": float(all(results.values())),
        "acceptance": rate(earlier + added),
        "earlier": rate(earlier),
        "own_tests": own_tests(),
        "checks": checks(),
        "changed_lines": changed_lines(),
    }
    print(json.dumps({"tests": results, **rewards}, indent=2))
    REWARD.parent.mkdir(parents=True, exist_ok=True)
    REWARD.write_text(json.dumps(rewards))


if __name__ == "__main__":
    main(*sys.argv[1:3])
