"""Offline tests for compare.py; run with python3 -m unittest evals.harbor.test_compare."""

import json
import tempfile
import unittest
from pathlib import Path

from evals.harbor import compare


def write_trial(job: Path, name: str, task: str, reward=None, exception=None, agent_ran=True) -> None:
    trial = job / name
    trial.mkdir(parents=True)
    trial.joinpath("result.json").write_text(json.dumps({
        "trial_name": name,
        "task_name": task,
        "verifier_result": None if reward is None else {"rewards": {"reward": reward}},
        "exception_info": None if exception is None else {"exception_type": exception},
        "agent_execution": {"started_at": "2026-10-30T00:00:00Z" if agent_ran else None},
    }))


class CompareTest(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())

    def test_scoring_rules(self):
        job = self.root / "job"
        write_trial(job, "a__1", "t/a", reward=1.0)
        write_trial(job, "a__2", "t/a", reward=0.0, exception="AgentTimeoutError")
        write_trial(job, "b__1", "t/b", exception="AgentTimeoutError")
        write_trial(job, "c__1", "t/c", exception="RuntimeError", agent_ran=False)
        write_trial(job, "d__1", "t/d", exception="NonZeroAgentExitCodeError", agent_ran=False)
        write_trial(job, "e__1", "t/e", reward=1.0)
        trials = {t.name: t for t in compare.load_trials([job], exclude={"e__1"})}
        self.assertEqual(trials["a__1"].score, 1.0)
        self.assertEqual(trials["a__2"].score, 0.0)
        self.assertEqual(trials["b__1"].score, 0.0)  # the agent ran out of time
        self.assertIsNone(trials["c__1"].score)  # the environment never started
        self.assertIsNone(trials["d__1"].score)  # setup failed before the agent ran
        self.assertEqual(trials["e__1"].reason, "excluded by request")

    def test_multi_step_trials(self):
        job = self.root / "job"
        step = {"agent_execution": {"started_at": "2026-11-06T00:00:00Z"}}
        for name, rewards, steps in (
            ("a__1", {"reward": 0.5, "checks": 1.0}, [step, step]),
            ("b__1", None, [{**step, "exception_info": {"exception_type": "AgentTimeoutError"}}]),
        ):
            trial = job / name
            for index in range(len(steps)):
                session = trial / "steps" / f"s{index}" / "agent" / "aice-session.jsonl"
                session.parent.mkdir(parents=True)
                session.touch()
            trial.joinpath("result.json").write_text(json.dumps({
                "trial_name": name, "task_name": "t/" + name[0], "agent_execution": None,
                "verifier_result": rewards and {"rewards": rewards}, "step_results": steps,
            }))
        trials = {t.name: t for t in compare.load_trials([job], set())}
        self.assertEqual(trials["a__1"].score, 0.5)
        self.assertEqual(trials["a__1"].metrics, {"checks": 1.0})
        self.assertEqual(len(trials["a__1"].sessions), 2)
        self.assertEqual(trials["b__1"].score, 0.0)  # a step's agent ran out of time

    def test_paired_difference_and_flips(self):
        base, cand = self.root / "base", self.root / "cand"
        for i in range(3):
            write_trial(base, f"a__{i}", "t/a", reward=1.0)
            write_trial(cand, f"a__{i}", "t/a", reward=1.0)
            write_trial(base, f"b__{i}", "t/b", reward=0.0)
            write_trial(cand, f"b__{i}", "t/b", reward=float(i > 0))
        write_trial(base, "c__0", "t/c", reward=1.0)
        report = compare.compare(compare.load_trials([base], set()), compare.load_trials([cand], set()), samples=2000)
        self.assertEqual(report["tasks"], 2)
        self.assertEqual(report["only_baseline"], ["t/c"])
        self.assertAlmostEqual(report["difference"], (2 / 3) / 2)
        low, high = report["interval95"]
        self.assertLessEqual(low, report["difference"])
        self.assertGreaterEqual(high, report["difference"])
        self.assertEqual(report["flips"], [{"task": "t/b", "baseline": "0/3", "candidate": "2/3"}])

    def test_identical_sides_have_no_difference(self):
        job = self.root / "job"
        write_trial(job, "a__0", "t/a", reward=1.0)
        write_trial(job, "b__0", "t/b", reward=0.0)
        trials = compare.load_trials([job], set())
        report = compare.compare(trials, trials, samples=500)
        self.assertEqual(report["difference"], 0)
        self.assertEqual(report["interval95"], (0, 0))
        self.assertEqual(report["flips"], [])


if __name__ == "__main__":
    unittest.main()
