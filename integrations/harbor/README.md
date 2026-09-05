# AICE Harbor adapter

Custom [Harbor](https://github.com/harbor-framework/harbor) installed agent for AICE. Harbor does not need an `AgentName` enum entry: pass the import path.

Run Harbor from the AICE repository root so `integrations.harbor.aice_agent:AiceAgent` is importable (`PYTHONPATH=.`).

PyPI `harbor` 0.22.x still uses `SUPPORTS_*` flags. Harbor `main` uses `AgentCapabilities()`; the adapter declares ATIF support through both compatible forms.

## Setup

```sh
pip install harbor
export PYTHONPATH=.
```

Credentials stay out of the command history by exporting them first, then forwarding with `--ae` (`--agent-env`).

## Examples

Terminal-Bench 2.0:

```sh
harbor run -d terminal-bench@2.0 \
  --agent integrations.harbor.aice_agent:AiceAgent \
  --model deepseek/deepseek-v4-flash \
  --ae AICE_DEEPSEEK_API_KEY="$AICE_DEEPSEEK_API_KEY" \
  -n 4
```

SWE-bench Lite:

```sh
harbor run -d swebench@lite \
  --agent integrations.harbor.aice_agent:AiceAgent \
  --model openai/gpt-5.6-terra \
  --ae OPENAI_API_KEY="$OPENAI_API_KEY" \
  -n 4
```

OpenCode Go catalog:

```sh
harbor run -d terminal-bench@2.0 \
  --agent integrations.harbor.aice_agent:AiceAgent \
  --model opencode-go/kimi-k2.6 \
  --ae AICE_OPENCODE_API_KEY="$AICE_OPENCODE_API_KEY" \
  --ae AICE_THINKING=high \
  -n 4
```

Any other Harbor `--model provider/model` is treated as AICE `custom`. Supply the OpenAI-compatible endpoint:

```sh
harbor run -d terminal-bench@2.0 \
  --agent integrations.harbor.aice_agent:AiceAgent \
  --model together/llama-3.3-70b \
  --ae AICE_CUSTOM_BASE_URL="https://api.together.xyz/v1" \
  --ae AICE_CUSTOM_API_KEY="$AICE_CUSTOM_API_KEY" \
  -n 4
```

Flags above are from Harbor `harbor run`: `-d`/`--dataset`, `--agent`/`-a`, `--model`/`-m`, `--ae`/`--agent-env`, `-n`/`--n-concurrent`.

## Model mapping

`--model` must be `provider/model`. The adapter sets `AICE_PROVIDER` / `AICE_MODEL` as follows:

| Harbor `--model` prefix | `AICE_PROVIDER` | `AICE_MODEL` | Typical `--ae` credential |
| --- | --- | --- | --- |
| `deepseek/` | `deepseek` | text after the first `/` | `AICE_DEEPSEEK_API_KEY` |
| `openai/` | `openai` | text after the first `/` | `OPENAI_API_KEY` |
| `opencode-go/` | `opencode-go` | text after the first `/` | `AICE_OPENCODE_API_KEY` |
| any other `provider/` | `custom` | text after the first `/` | `AICE_CUSTOM_API_KEY` and `AICE_CUSTOM_BASE_URL` |

The adapter also sets `AICE_NO_DEP_INSTALL=1` and `AICE_NO_UPDATE_CHECK=1` (Harbor preinstalls `rg`). Optional passthrough when present: `AICE_THINKING`, `AICE_CUSTOM_BASE_URL`, and the credential variables in the table.

## Pin an AICE release

`install.sh` reads `AICE_VERSION` (it adds a `v` prefix when missing). Set
`AICE_SESSION_V3_RELEASE` to an actual release containing the Session v3 writer,
then pass the adapter constructor kwarg:

```sh
harbor run -d terminal-bench@2.0 \
  --agent integrations.harbor.aice_agent:AiceAgent \
  --model deepseek/deepseek-v4-flash \
  --ae AICE_DEEPSEEK_API_KEY="$AICE_DEEPSEEK_API_KEY" \
  --ak version="$AICE_SESSION_V3_RELEASE" \
  -n 4
```

`--ak` / `--agent-kwarg` is Harbor's constructor-kwarg flag (`key=value`). The
adapter requires a build that writes **Session format v3**, together with print
NDJSON and explicit print Sessions. No release number is assumed to include
that format. Omitting the kwarg installs the latest GitHub release; verify it
supports v3 first, or use a trial image with a matching source build and an
installation override. Older Session formats are explicitly rejected during
conversion and their files are left untouched.

## Runtime

The adapter installs AICE with `scripts/install.sh` into `/usr/local/bin/aice`, then runs:

```text
aice --workspace . --print --yolo --approve \
  --output-format json \
  --session /logs/agent/aice-session.jsonl \
  -- <instruction> \
  2>&1 </dev/null | stdbuf -oL tee /logs/agent/aice.txt
```

`--yolo` auto-allows Guard `ask` decisions; `--approve` trusts project
`AGENTS.md` / `.aice` prompt files for that run. The line-buffered NDJSON stream
is teed to `/logs/agent/aice.txt`, while the native append-only Session is saved
as `/logs/agent/aice-session.jsonl`. After the run, the adapter converts the
Session into ATIF-v1.7 `trajectory.json` and fills Harbor's token and cost
fields. Conversion keeps source messages from every recorded branch in physical
append order and adds labeled compaction steps. Each assistant's usage and each
compaction's usage contribute once to the totals. Tool results are attached to
their parent-chain tool group, so repeated model tool-call IDs do not mix rounds
or branches. Pending calls have no invented observation; recovery results retain
the source's unknown-outcome text. An incomplete final physical record is ignored
without truncating the native log; malformed complete JSON records are rejected.
The converter is a projection, not a replacement for the native Store's complete
replay validation.

During a run, follow progress from the host with:

```sh
tail -f jobs/<job>/<trial>/agent/aice.txt
```

`aice.txt` can also contain AICE stderr diagnostics because the runtime merges
both streams. Machine consumers use `aice-session.jsonl` or `trajectory.json`,
not the tee log.

## Known limitations

- No native resume, ATIF/native trajectory loading, or handoff.

## Offline converter tests

```sh
python3 -m unittest integrations.harbor.test_aice_agent -v
```

These standard-library tests use small Harbor model stand-ins and v3 record
fixtures. They exercise conversion and accounting without credentials, model
calls, or installing Harbor. They do not validate a particular Harbor release's
Pydantic/ATIF schema; run a real Harbor integration check separately when that
dependency is available.
