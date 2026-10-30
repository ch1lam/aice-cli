"""Compare two sets of Harbor job directories: a baseline and a candidate build.

Usage (from the repository root):

    python3 evals/harbor/compare.py --baseline jobs/A [jobs/A2 ...] \
        --candidate jobs/B [...] [--exclude TRIAL ...] [--json report.json]

Each trial is scored from its verifier reward. A trial without a reward is
scored 0 when the agent ran and then failed or timed out; otherwise it is an
infrastructure failure and excluded. --exclude drops named trials, for example
those that overlapped a host sleep. Process metrics come from each trial's
native Session through `aice session stats` (--aice names the command).

The pass-rate difference is the mean over tasks scored on both sides of
candidate minus baseline pass rate, with a 95% interval from a two-level
bootstrap that resamples tasks, then trials within each task.
"""

from __future__ import annotations

import argparse
import json
import random
import shlex
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from statistics import mean

# Exceptions after the agent started that are the agent's own failure.
AGENT_FAILURES = {"AgentTimeoutError", "NonZeroAgentExitCodeError"}
METRICS = ("cost_usd", "model_requests", "failed_requests", "tool_calls", "tool_errors", "duration_s")


@dataclass
class Trial:
    name: str
    task: str
    score: float | None  # None when excluded
    reason: str = ""
    session: Path | None = None
    metrics: dict[str, float] = field(default_factory=dict)
    builds: list[str] = field(default_factory=list)


def load_trials(job_dirs: list[Path], exclude: set[str]) -> list[Trial]:
    trials = []
    for job in job_dirs:
        for result_path in sorted(job.glob("*/result.json")):
            result = json.loads(result_path.read_text())
            name = result["trial_name"]
            task = result["task_name"]
            session = result_path.parent / "agent" / "aice-session.jsonl"
            trial = Trial(name, task, None, session=session if session.is_file() else None)
            reward = ((result.get("verifier_result") or {}).get("rewards") or {}).get("reward")
            exception = (result.get("exception_info") or {}).get("exception_type")
            agent_ran = bool((result.get("agent_execution") or {}).get("started_at"))
            if name in exclude:
                trial.reason = "excluded by request"
            elif reward is not None:
                trial.score = float(reward)
            elif agent_ran and exception in AGENT_FAILURES:
                trial.score = 0.0
            else:
                trial.reason = exception or "no reward"
            trials.append(trial)
    return trials


def attach_stats(trials: list[Trial], aice: list[str]) -> None:
    sessions = [t for t in trials if t.session is not None]
    if not sessions:
        return
    output = subprocess.run(
        [*aice, "session", "stats", *(str(t.session) for t in sessions)],
        check=True, capture_output=True, text=True,
    ).stdout
    for trial, line in zip(sessions, output.splitlines(), strict=True):
        stats = json.loads(line)
        cost = (stats["usage"].get("cost") or {}).get("total", 0.0)
        trial.builds = stats["builds"]
        trial.metrics = {
            "cost_usd": cost,
            "model_requests": stats["model_requests"],
            "failed_requests": stats["failed_requests"],
            "tool_calls": stats["tool_calls"],
            "tool_errors": stats["tool_errors"],
            "duration_s": stats["duration_ms"] / 1000,
        }


def by_task(trials: list[Trial]) -> dict[str, list[Trial]]:
    tasks: dict[str, list[Trial]] = {}
    for trial in trials:
        if trial.score is not None:
            tasks.setdefault(trial.task, []).append(trial)
    return tasks


def bootstrap_interval(base: dict[str, list[float]], cand: dict[str, list[float]], samples: int, seed: int) -> tuple[float, float]:
    rng = random.Random(seed)
    tasks = sorted(base)
    diffs = []
    for _ in range(samples):
        total = 0.0
        for _ in tasks:
            task = rng.choice(tasks)
            total += mean(rng.choices(cand[task], k=len(cand[task]))) - mean(rng.choices(base[task], k=len(base[task])))
        diffs.append(total / len(tasks))
    diffs.sort()
    return diffs[int(0.025 * samples)], diffs[int(0.975 * samples) - 1]


def compare(baseline: list[Trial], candidate: list[Trial], samples: int = 10000, seed: int = 0) -> dict:
    base_tasks, cand_tasks = by_task(baseline), by_task(candidate)
    shared = sorted(set(base_tasks) & set(cand_tasks))
    base_scores = {t: [x.score for x in base_tasks[t]] for t in shared}
    cand_scores = {t: [x.score for x in cand_tasks[t]] for t in shared}
    report: dict = {
        "tasks": len(shared),
        "only_baseline": sorted(set(base_tasks) - set(cand_tasks)),
        "only_candidate": sorted(set(cand_tasks) - set(base_tasks)),
        "excluded": [
            {"side": side, "trial": t.name, "reason": t.reason}
            for side, trials in (("baseline", baseline), ("candidate", candidate))
            for t in trials if t.score is None
        ],
        "builds": {
            side: sorted({b for t in trials for b in t.builds})
            for side, trials in (("baseline", baseline), ("candidate", candidate))
        },
    }
    if not shared:
        return report
    rates = {t: (mean(base_scores[t]), mean(cand_scores[t])) for t in shared}
    report["baseline_pass_rate"] = mean(b for b, _ in rates.values())
    report["candidate_pass_rate"] = mean(c for _, c in rates.values())
    report["difference"] = report["candidate_pass_rate"] - report["baseline_pass_rate"]
    report["interval95"] = bootstrap_interval(base_scores, cand_scores, samples, seed)
    report["flips"] = [
        {"task": t, "baseline": f"{sum(base_scores[t]):g}/{len(base_scores[t])}", "candidate": f"{sum(cand_scores[t]):g}/{len(cand_scores[t])}"}
        for t in sorted(shared, key=lambda t: rates[t][0] - rates[t][1])
        if rates[t][0] != rates[t][1]
    ]
    metrics = {}
    for name in METRICS:
        diffs = []
        for t in shared:
            base_values = [x.metrics[name] for x in base_tasks[t] if name in x.metrics]
            cand_values = [x.metrics[name] for x in cand_tasks[t] if name in x.metrics]
            if base_values and cand_values:
                diffs.append(mean(cand_values) - mean(base_values))
        if diffs:
            metrics[name] = {"mean_paired_difference": mean(diffs), "tasks": len(diffs)}
    report["metrics"] = metrics
    for side, tasks in (("baseline", base_tasks), ("candidate", cand_tasks)):
        trials = [x for t in shared for x in tasks[t]]
        solved = sum(x.score for x in trials)
        cost = sum(x.metrics.get("cost_usd", 0.0) for x in trials)
        report[f"{side}_cost_per_solved_usd"] = cost / solved if solved else None
    return report


def render(report: dict) -> str:
    lines = [f"Builds: baseline {report['builds']['baseline']}, candidate {report['builds']['candidate']}"]
    lines.append(f"Tasks scored on both sides: {report['tasks']}")
    if "difference" in report:
        low, high = report["interval95"]
        lines.append(
            f"Pass rate: baseline {report['baseline_pass_rate']:.3f}, candidate {report['candidate_pass_rate']:.3f}, "
            f"difference {report['difference']:+.3f} (95% interval {low:+.3f} to {high:+.3f})"
        )
        for side in ("baseline", "candidate"):
            value = report[f"{side}_cost_per_solved_usd"]
            lines.append(f"Cost per solved task, {side}: " + ("n/a" if value is None else f"${value:.4f}"))
        if report["flips"]:
            lines.append("Tasks whose pass rate changed (baseline -> candidate):")
            lines += [f"  {f['task']}: {f['baseline']} -> {f['candidate']}" for f in report["flips"]]
        if report["metrics"]:
            lines.append("Process metrics, mean paired difference per task (candidate - baseline):")
            lines += [f"  {name}: {m['mean_paired_difference']:+.4g} over {m['tasks']} tasks" for name, m in report["metrics"].items()]
    for key in ("only_baseline", "only_candidate"):
        if report[key]:
            lines.append(f"Tasks scored only in {key.removeprefix('only_')}: {', '.join(report[key])}")
    if report["excluded"]:
        lines.append("Excluded trials:")
        lines += [f"  {e['side']} {e['trial']}: {e['reason']}" for e in report["excluded"]]
    return "\n".join(lines)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--baseline", nargs="+", type=Path, required=True)
    parser.add_argument("--candidate", nargs="+", type=Path, required=True)
    parser.add_argument("--exclude", nargs="*", default=[], help="trial names to drop")
    parser.add_argument("--aice", default="go run ./cmd/aice", help="command that runs AICE")
    parser.add_argument("--samples", type=int, default=10000)
    parser.add_argument("--json", type=Path, help="also write the report as JSON")
    args = parser.parse_args(argv)
    exclude = set(args.exclude)
    baseline, candidate = load_trials(args.baseline, exclude), load_trials(args.candidate, exclude)
    attach_stats(baseline + candidate, shlex.split(args.aice))
    report = compare(baseline, candidate, samples=args.samples)
    print(render(report))
    if args.json:
        args.json.write_text(json.dumps(report, indent=2) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
