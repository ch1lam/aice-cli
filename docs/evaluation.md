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
at most 2 CPUs and 4 GiB memory. The rule is reproducible and independent of
AICE results. The full dataset is the acceptance set and runs only at
milestones.

## Trajectory fidelity

Harbor derives ATIF from ACP updates: `function_name` is the ACP tool `kind`,
`arguments` the tool's `rawInput`, and cost comes from `usage_update`. Steps
have no timestamps or per-step usage. The native Session in the same trial is
the complete record.

## Maintenance tasks

The [Go service](../evals/go-service/README.md) and
[Python CLI](../evals/python-cli/README.md) fixtures evaluate successive
maintenance changes with their own drivers.
