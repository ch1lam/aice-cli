# Evaluation

AICE is evaluated as an ordinary [ACP](acp.md) agent. [Harbor](https://github.com/harbor-framework/harbor)
owns tasks, sandboxes, verifiers and the ATIF trajectory; AICE knows nothing
about Harbor and exposes only the protocol and its own Session. Every trial
keeps the native Session, so a result can be traced to each model input with
`session.RebuildRequest` ([Sessions](execution-sessions.md#sessions)).

## Running AICE in Harbor

[`evals/harbor/aice_acp.py`](../evals/harbor/aice_acp.py) subclasses Harbor's
generic `AcpAgent`. Harbor's binary distribution only downloads HTTPS archives,
so the subclass uploads a local build instead and runs:

```text
aice acp --workspace . --approve --session /logs/agent/aice-session.jsonl
```

`/logs/agent` is the trial's `agent/` directory, so the Session returns to the
host beside Harbor's `acp-events.jsonl` and `trajectory.json`. Harbor's `-m
provider/model` sets `AICE_PROVIDER` and `AICE_MODEL`; the runner then selects
the same value through the ACP model option. Everything else, including the
ACP client and ATIF generation, is Harbor's.

Harbor runs its ACP client inside each task container. The subclass installs
that client's Python venv and CA certificates only when missing, without
recommended packages: Harbor's default installs `python3-pip` with a compiler
toolchain on every trial, which exceeded the agent setup timeout on a slow
package mirror. At startup AICE downloads its checksum-pinned helpers
([runtime helpers](installation.md#runtime-helpers)) into the container like on
any host, so tool behavior matches ordinary use.

Build both Linux binaries (ignored by git), then run from the repository root
with the provider key already exported in the shell. The single-quoted
`${...}` keeps the key out of the command line and the job directory; Harbor
resolves it from the environment when the trial starts.

```sh
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -o evals/harbor/bin/aice-linux-$arch ./cmd/aice
done
PYTHONPATH=. harbor run -c evals/harbor/tb21-dev.yaml \
  -a evals.harbor.aice_acp:AiceAcpAgent \
  -m opencode-go/deepseek-v4.1-flash \
  --ae 'AICE_OPENCODE_API_KEY=${AICE_OPENCODE_API_KEY}' \
  --ae AICE_THINKING=high \
  -n 4 -o jobs
```

`--ak binary_dir=<dir>` uploads binaries from another directory, for example a
baseline build. Validate a trajectory with Harbor's checker:

```sh
python -m harbor.utils.trajectory_validator jobs/<job>/<trial>/agent/trajectory.json
```

## Task sets

[`evals/harbor/tb21-dev.yaml`](../evals/harbor/tb21-dev.yaml) pins
Terminal-Bench 2.1 by digest and lists the development set: the 25 tasks whose
`sha256(name)` sorts first among tasks with an agent timeout of at most 1800 s,
at most 2 CPUs and 4 GiB memory, excluding tasks whose image cannot host
Harbor's ACP client (`qemu-alpine-ssh`: Debian 11 with Python 3.9; the client
needs 3.10). The rule is reproducible and independent of AICE results. The
full dataset is the acceptance set and runs only at milestones.

Harbor's agent, verifier and environment timeouts count wall-clock time, so a
host that sleeps during a job turns in-flight trials into timeouts and failed
model requests. Keep the host awake, and exclude trials that overlap a sleep
rather than scoring them.

## Comparing builds

An experiment runs a baseline and a candidate build on the same task set,
model and settings, each with `-k 3` or more attempts per task, and compares
them with [`evals/harbor/compare.py`](../evals/harbor/compare.py). Build the
baseline from a separate worktree so both binaries exist at once; each build
records itself in every Session as `dev+<revision>`, marked `-dirty` for
uncommitted changes, and the report lists the builds it found.

```sh
git worktree add ../aice-baseline <baseline-commit>
(cd ../aice-baseline && for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -o "$OLDPWD/evals/harbor/bin-baseline/aice-linux-$arch" ./cmd/aice
done)
# Run the dev set twice, adding --ak binary_dir=evals/harbor/bin-baseline
# for the baseline and -k 3 -n 6 to both runs, then:
python3 evals/harbor/compare.py --baseline jobs/<baseline-job> \
  --candidate jobs/<candidate-job> [--exclude <trial> ...] [--json report.json]
```

A trial scores its verifier reward. Without a reward it scores 0 when the
agent ran and then timed out or exited with an error, and is otherwise an
infrastructure failure, excluded and listed. `--exclude` drops further trials,
such as those that overlapped a host sleep. The report gives:

- pass rates over the tasks scored on both sides, and their difference with a
  95% interval from a two-level bootstrap (tasks, then trials within a task);
- tasks whose pass rate changed;
- cost per solved task;
- mean paired per-task differences of process metrics from each trial's
  Session through `aice session stats`
  ([Sessions](execution-sessions.md#sessions)): cost, model requests, failed
  requests, tool calls, tool errors and duration.

Command timeouts inside a trial are not structured in the Session; a trial
timeout appears as the agent failure that scored it.

## Noise floor

An A/A comparison runs the same build as both baseline and candidate. Its
interval and changed tasks show how much two identical builds differ, and a
candidate's difference is only evidence when it clearly exceeds that. No A/A
run is recorded yet; the result belongs here once one runs.

## Replaying a decision

A single model decision can be rerun without a full trial. A case in
[`evals/replay/cases.jsonl`](../evals/replay/cases.jsonl) names a trial's
Session (relative to the `jobs` directory, with its SHA-256), the assistant
message to replay, and rules for the replacement reply:

| Rule | Passes when |
| --- | --- |
| `call` | some tool call (of `tool`, when given) has arguments matching `pattern` |
| `no_call` | no such tool call exists |
| `text` / `no_text` | the reply's visible text matches / does not match `pattern` |

Arguments are matched as their JSON text with `<`, `>` and `&` unescaped.
The runner rebuilds the message's request from the Session with
`session.RebuildRequest`, checks that the recorded reply breaks a rule, then
calls the configured model once per run and judges each reply:

```sh
AICE_REPLAY_EVAL=1 AICE_PROVIDER=opencode-go AICE_MODEL=deepseek-v4.1-flash \
  AICE_REPLAY_PROMPT=current AICE_REPLAY_TOOLS=current \
  go test -tags=integration ./internal/app -run TestReplayEval -v
```

`AICE_REPLAY_PROMPT` and `AICE_REPLAY_TOOLS` choose `recorded` (default) or
`current`. Current takes this build's built-in system prompt base, up to its
workspace line, and the tool set `aice acp` offers without configuration; the
recorded workspace line, project instructions and Skills catalog are kept. On a
Session recorded by the same build, current reproduces the recorded request
exactly (`TestReplayCurrentPartsReproduceSameBuild`). Configuration comes from
the environment in an empty home. `AICE_REPLAY_RUNS` (default 3),
`AICE_REPLAY_FILTER` and `AICE_REPLAY_SESSIONS` adjust the run; results are
written to `evals/replay/results/`. Cases keep only identifiers and rules:
benchmark Sessions stay in the ignored `jobs` directory and are not committed.

## Trajectory fidelity

Harbor derives ATIF from ACP updates: `function_name` is the ACP tool `kind`,
`arguments` the tool's `rawInput`, and cost comes from `usage_update`. Steps
have no timestamps or per-step usage. The native Session in the same trial is
the complete record.

## Maintenance tasks

The [Go service](../evals/go-service/README.md) and
[Python CLI](../evals/python-cli/README.md) fixtures evaluate successive
maintenance changes with their own drivers.
