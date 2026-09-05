#!/usr/bin/env python3
"""Run offline HTTP acceptance against isolated source copies, including negative controls."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True
from prepare import ROOT, prepare

STAGES = {
    '1': '^Test(Base|Unicode)',
    '2': '^Test(Base|Unicode|Tags)',
    '3': '^Test(Base|Unicode|Tags)',
    '4': '^Test',
    'refactor': '^Test(Base|Unicode|Tags)',
}


def run(candidate, pattern, expected_failure=None):
    with tempfile.TemporaryDirectory(prefix='aice-go-acceptance-') as tmp:
        workspace = Path(tmp) / 'candidate'
        shutil.copytree(candidate, workspace, ignore=shutil.ignore_patterns('.git', '__pycache__'))
        acceptance = workspace / 'aice_acceptance'
        # Never allow submitted code to substitute evaluator-owned tests.
        if acceptance.exists():
            shutil.rmtree(acceptance)
        acceptance.mkdir()
        shutil.copyfile(ROOT / 'acceptance/service_test.go.txt', acceptance / 'service_test.go')
        env = dict(os.environ, GOWORK='off', GOPROXY='off', GOSUMDB='off', GOTOOLCHAIN='local')
        command = ['go', 'test', '-race', '-count=1', '-timeout=30s', '-json', '-run', pattern, './...']
        result = subprocess.run(command, cwd=workspace, env=env, capture_output=True, text=True, timeout=90)
        events = []
        for line in result.stdout.splitlines():
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                pass
        failed = {e['Test'] for e in events if e.get('Action') == 'fail' and 'Test' in e}
        passed = {e['Test'] for e in events if e.get('Action') == 'pass' and 'Test' in e}
        build_failure = any(e.get('Action') == 'build-fail' or e.get('FailedBuild') for e in events)
        if expected_failure:
            ok = result.returncode != 0 and failed == {expected_failure} and not build_failure
        else:
            ok = result.returncode == 0 and bool(passed)
        if not ok:
            print(result.stdout)
            print(result.stderr)
            raise RuntimeError(f"unexpected acceptance result: failed={sorted(failed)}, exit={result.returncode}")
        label = f"expected failure: {expected_failure}" if expected_failure else f"{len(passed)} tests passed"
        print(f"PASS {candidate.name}: {label} [race, uncached]", flush=True)


def self_check():
    with tempfile.TemporaryDirectory(prefix='aice-go-seeds-') as tmp:
        seeds = {}
        for kind in ['extension', 'bug', 'refactor', 'reference']:
            seeds[kind] = Path(tmp) / kind
            prepare(kind, seeds[kind])
        run(seeds['extension'], STAGES['1'])
        run(seeds['extension'], '^TestTags$', 'TestTags')
        run(seeds['bug'], '^Test(Base|Tags)')
        run(seeds['bug'], '^TestUnicodeBoundary$', 'TestUnicodeBoundary')
        run(seeds['refactor'], STAGES['refactor'])
        run(seeds['refactor'], '^TestLimit$', 'TestLimit')
        run(seeds['reference'], STAGES['4'])
    print('Fixture validation passed; no model was evaluated.', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', type=Path)
    parser.add_argument('--stage', choices=STAGES, default='4')
    args = parser.parse_args()
    if args.candidate:
        run(args.candidate.resolve(), STAGES[args.stage])
    else:
        self_check()
