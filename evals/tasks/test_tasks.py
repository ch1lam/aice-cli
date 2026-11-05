"""Offline checks of the maintenance tasks; run with python3 -m unittest evals.tasks.test_tasks.

The reference must pass every acceptance case and each bug-fix seed must fail
exactly the cases its defect breaks. A Harbor oracle run checks the same
inside the task images.
"""

import importlib.util
import shutil
import tomllib
import unittest
from pathlib import Path

TASKS = Path(__file__).resolve().parent
SHARED = {
    "go-notes-bugfix": ("go-notes", ["tests/verify.py", "tests/service_test.go.txt"]),
    "python-ledger-bugfix": ("python-ledger", ["tests/verify.py"]),
}


def load_verifier(task: str):
    spec = importlib.util.spec_from_file_location(f"verify_{task.replace('-', '_')}", TASKS / task / "tests" / "verify.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class TaskLayoutTest(unittest.TestCase):
    def test_shared_files_are_identical(self):
        for task, (source, names) in SHARED.items():
            for name in names:
                self.assertEqual((TASKS / task / name).read_bytes(), (TASKS / source / name).read_bytes(), f"{task}/{name}")

    def test_steps_are_complete(self):
        for config in TASKS.glob("*/task.toml"):
            steps = tomllib.loads(config.read_text()).get("steps")
            if not steps:
                self.assertTrue((config.parent / "instruction.md").is_file(), config)
                self.assertTrue((config.parent / "tests" / "test.sh").is_file(), config)
                continue
            for step in steps:
                directory = config.parent / "steps" / step["name"]
                for name in ("instruction.md", "tests/test.sh", "workdir/setup.sh"):
                    self.assertTrue((directory / name).is_file(), directory / name)


class PythonLedgerTest(unittest.TestCase):
    def setUp(self):
        self.verify = load_verifier("python-ledger")

    def failures(self, workspace: Path) -> set[str]:
        self.verify.WORKSPACE = workspace
        earlier, added = self.verify.cases_for("python-ledger", "prefix")
        return {name for name, case in (earlier | added).items() if not self.verify.passes(name, case)}

    def test_reference_passes(self):
        self.assertEqual(self.failures(TASKS / "python-ledger" / "solution" / "reference"), set())

    def test_seed_fails_only_precision(self):
        self.assertEqual(self.failures(TASKS / "python-ledger-bugfix" / "environment" / "app"), {"large-exact-cents", "permutation", "maximum-amount"})


@unittest.skipIf(shutil.which("go") is None, "Go is not installed")
class GoNotesTest(unittest.TestCase):
    def setUp(self):
        self.verify = load_verifier("go-notes")

    def failures(self, workspace: Path) -> set[str]:
        self.verify.WORKSPACE = workspace
        earlier, added = self.verify.tests_for("go-notes", "limit")
        return {name for name, ok in self.verify.acceptance(earlier + added).items() if not ok}

    def test_reference_passes(self):
        self.assertEqual(self.failures(TASKS / "go-notes" / "solution" / "reference"), set())

    def test_seed_fails_only_unicode(self):
        self.assertEqual(self.failures(TASKS / "go-notes-bugfix" / "environment" / "app"), {"TestUnicodeBoundary"})


if __name__ == "__main__":
    unittest.main()
