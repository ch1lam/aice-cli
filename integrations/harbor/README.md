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

`install.sh` reads `AICE_VERSION` (it adds a `v` prefix when missing). Pass the adapter constructor kwarg:

```sh
harbor run -d terminal-bench@2.0 \
  --agent integrations.harbor.aice_agent:AiceAgent \
  --model deepseek/deepseek-v4-flash \
  --ae AICE_DEEPSEEK_API_KEY="$AICE_DEEPSEEK_API_KEY" \
  --ak version=v0.11.0 \
  -n 4
```

`--ak` / `--agent-kwarg` is Harbor's constructor-kwarg flag (`key=value`). The
adapter requires AICE v0.11.0 or later for print NDJSON and explicit print
Sessions. Omit the kwarg to install the latest GitHub release.

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
fields.

During a run, follow progress from the host with:

```sh
tail -f jobs/<job>/<trial>/agent/aice.txt
```

`aice.txt` can also contain AICE stderr diagnostics because the runtime merges
both streams. Machine consumers use `aice-session.jsonl` or `trajectory.json`,
not the tee log.

## Known limitations

- No native resume, ATIF/native trajectory loading, or handoff.
