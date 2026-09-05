"""Harbor installed-agent adapter for AICE."""

from __future__ import annotations

import json
import re
import shlex
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, override

from harbor.agents.installed.base import (
    BaseInstalledAgent,
    NonZeroAgentExitCodeError,
    with_prompt_template,
)
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext
from harbor.models.trajectories import (
    Agent,
    FinalMetrics,
    Metrics,
    ObservationResult,
    Step,
    ToolCall,
    Trajectory,
)

try:
    from harbor.agents.capabilities import AgentCapabilities
except ImportError:  # Harbor 0.22.x still uses SUPPORTS_* class flags.
    AgentCapabilities = None

_INSTALL_SCRIPT_URL = (
    "https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.sh"
)
_NATIVE_PROVIDERS = frozenset({"deepseek", "openai", "opencode-go"})
_PASSTHROUGH_ENV = (
    "AICE_DEEPSEEK_API_KEY",
    "AICE_OPENCODE_API_KEY",
    "OPENAI_API_KEY",
    "AICE_CUSTOM_API_KEY",
    "AICE_CUSTOM_BASE_URL",
    "AICE_THINKING",
)
_EXIT_CODE_RE = re.compile(r"Command failed \(exit (\d+)\)")


class AiceAgent(BaseInstalledAgent):
    """Install and run AICE inside a Harbor trial container."""

    SUPPORTS_ATIF = True
    if AgentCapabilities is not None:
        capabilities = AgentCapabilities(atif=True)

    def __init__(
        self,
        logs_dir: Path,
        *args: Any,
        version: str | None = None,
        **kwargs: Any,
    ) -> None:
        super().__init__(logs_dir, *args, version=version, **kwargs)
        self._exit_code = 0
        self._run_error: str | None = None

    @staticmethod
    @override
    def name() -> str:
        return "aice"

    @override
    def get_version_command(self) -> str | None:
        return "aice --version"

    def _build_env(self) -> dict[str, str]:
        provider = self._parsed_model_provider
        model = self._parsed_model_name
        if not provider or not model:
            raise ValueError(
                "AICE Harbor adapter requires --model in 'provider/model' form, "
                f"got {self.model_name!r}"
            )

        aice_provider = provider if provider in _NATIVE_PROVIDERS else "custom"
        env: dict[str, str] = {
            "AICE_PROVIDER": aice_provider,
            "AICE_MODEL": model,
            "AICE_NO_DEP_INSTALL": "1",
            "AICE_NO_UPDATE_CHECK": "1",
        }
        for key in _PASSTHROUGH_ENV:
            value = self._get_env(key)
            if value is not None:
                env[key] = value
        return env

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        await self.ensure_system_dependencies(
            environment,
            ("curl", "ca_certificates", "coreutils", "ripgrep", "tar"),
        )

        version_env = (
            f"AICE_VERSION={shlex.quote(self._version)} "
            if self._version
            else ""
        )
        # Install into /usr/local/bin (world-executable PATH). A symlink into
        # /root/.local/bin would be unreadable by the agent user (home 0700).
        await self.exec_as_root(
            environment,
            command=(
                f"curl -fsSL {shlex.quote(_INSTALL_SCRIPT_URL)} | "
                f"{version_env}INSTALL_DIR=/usr/local/bin sh && "
                "chmod 755 /usr/local/bin/aice"
            ),
        )

    @override
    @with_prompt_template
    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        escaped_instruction = shlex.quote(instruction)
        log_file = shlex.quote((self.environment_logs_dir / "aice.txt").as_posix())
        session_file = shlex.quote(
            (self.environment_logs_dir / "aice-session.jsonl").as_posix()
        )
        command = (
            "aice --workspace . --print --yolo --approve "
            f"--output-format json --session {session_file} -- "
            f"{escaped_instruction} 2>&1 </dev/null | stdbuf -oL tee {log_file}"
        )

        self._exit_code = 0
        self._run_error = None
        try:
            await self.exec_as_agent(
                environment,
                command=command,
                env=self._build_env(),
            )
        except NonZeroAgentExitCodeError as exc:
            self._run_error = str(exc)
            match = _EXIT_CODE_RE.search(self._run_error)
            self._exit_code = int(match.group(1)) if match else 1
            raise
        except Exception as exc:
            self._exit_code = 1
            self._run_error = str(exc)
            raise

    @override
    def populate_context_post_run(self, context: AgentContext) -> None:
        metadata: dict[str, Any] = {"exit_code": self._exit_code}
        if self._run_error is not None:
            metadata["error"] = self._run_error
        context.metadata = metadata

        try:
            trajectory = self._session_to_trajectory(
                self.logs_dir / "aice-session.jsonl"
            )
            if trajectory is None:
                return

            final_metrics = trajectory.final_metrics
            if final_metrics is not None:
                context.n_input_tokens = final_metrics.total_prompt_tokens or 0
                context.n_cache_tokens = final_metrics.total_cached_tokens or 0
                context.n_output_tokens = final_metrics.total_completion_tokens or 0
                context.cost_usd = final_metrics.total_cost_usd

            trajectory_path = self.logs_dir / "trajectory.json"
            trajectory_path.write_text(
                json.dumps(trajectory.to_json_dict(), indent=2) + "\n",
                encoding="utf-8",
            )
        except Exception:
            self.logger.exception("Failed to convert AICE session to ATIF")

    def _session_to_trajectory(self, path: Path) -> Trajectory | None:
        if not path.exists():
            self.logger.debug("AICE session file does not exist: %s", path)
            return None

        records = _read_session_records(path)
        session_id = records[0]["id"]
        step_records: list[dict[str, Any]] = []
        nodes: dict[str, dict[str, Any]] = {}
        tool_steps: dict[tuple[str, str], dict[str, Any]] = {}
        total_prompt_tokens = 0
        total_completion_tokens = 0
        total_cached_tokens = 0
        total_cost_usd = 0.0
        has_cost = False

        for record in records[1:]:
            record_type = record["type"]
            if record_type == "leaf":
                continue
            nodes[record["id"]] = record
            is_compaction = record_type == "compaction"
            message = record if is_compaction else record["message"]
            role = "assistant" if is_compaction else message.get("role")
            if role == "user":
                step_records.append({
                    "step_id": len(step_records) + 1,
                    "source": "user",
                    "message": _content_text(message.get("content"), "text"),
                })
                continue
            if role == "assistant":
                usage = message.get("usage")
                if not isinstance(usage, dict):
                    usage = {}
                prompt_tokens, completion_tokens, cached_tokens, cost_usd = (
                    _atif_usage(usage)
                )
                total_prompt_tokens += prompt_tokens
                total_completion_tokens += completion_tokens
                total_cached_tokens += cached_tokens
                if cost_usd is not None:
                    total_cost_usd += cost_usd
                    has_cost = True
                tool_calls = [] if is_compaction else _atif_tool_calls(message.get("content"))
                step_record: dict[str, Any] = {
                    "step_id": len(step_records) + 1,
                    "timestamp": _atif_timestamp(
                        record.get("created_at") if is_compaction else message.get("timestamp")
                    ),
                    "source": "agent",
                    "model_name": message.get("response_model") or message.get("model"),
                    "message": (
                        "[Context compaction]\n" + message.get("summary", "")
                        if is_compaction else _content_text(message.get("content"), "text")
                    ),
                    "reasoning_content": _content_text(message.get("content"), "thinking") or None,
                    "tool_calls": tool_calls or None,
                    "metrics": Metrics(
                        prompt_tokens=prompt_tokens,
                        completion_tokens=completion_tokens,
                        cached_tokens=cached_tokens,
                        cost_usd=cost_usd,
                    ),
                    "llm_call_count": 1,
                }
                step_records.append(step_record)
                for tool_call in tool_calls:
                    tool_steps[(record["id"], tool_call.tool_call_id)] = step_record
                continue
            if role != "toolResult":
                raise ValueError(f"Unsupported AICE source message role: {role!r}")

            tool_call_id = message.get("tool_call_id")
            group_id = _tool_group(record, nodes, tool_call_id)
            step_record = tool_steps.get((group_id, tool_call_id))
            if step_record is None:
                raise ValueError(f"Unmatched AICE tool result in record {record['id']!r}")
            observation = step_record.setdefault("observation", {"results": []})
            observation["results"].append(ObservationResult(
                source_call_id=tool_call_id,
                content=_content_text(message.get("content"), "text"),
            ))

        if not step_records:
            self.logger.debug("AICE session produced no ATIF steps: %s", path)
            return None

        steps = [Step(**record) for record in step_records]
        return Trajectory(
            schema_version="ATIF-v1.7",
            session_id=session_id or "unknown",
            agent=Agent(
                name="aice",
                version=self.version() or "unknown",
                model_name=self.model_name,
            ),
            steps=steps,
            final_metrics=FinalMetrics(
                total_prompt_tokens=total_prompt_tokens,
                total_completion_tokens=total_completion_tokens,
                total_cached_tokens=total_cached_tokens,
                total_cost_usd=total_cost_usd if has_cost else None,
                total_steps=len(steps),
            ),
        )


def _read_session_records(path: Path) -> list[dict[str, Any]]:
    """Read v3 without rewriting recovery tails or interpreting legacy turns."""
    records: list[dict[str, Any]] = []
    ids: set[str] = set()
    nodes: set[str] = set()
    with path.open("rb") as source:
        for line_number, line in enumerate(source, start=1):
            if not line.endswith(b"\n"):
                # Store recovery discards an incomplete final physical record.
                break
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"Invalid AICE session line {line_number}") from exc
            if not isinstance(record, dict):
                raise ValueError(f"Invalid AICE session record on line {line_number}")
            if not records:
                if record.get("type") != "session" or type(record.get("version")) is not int or record["version"] != 3:
                    raise ValueError("AICE Harbor adapter requires Session format v3; older formats are unsupported")
            elif record.get("type") not in {"message", "compaction", "leaf"}:
                raise ValueError(f"Unsupported AICE record type on line {line_number}")
            record_id = record.get("id")
            if not isinstance(record_id, str) or not record_id or (records and record_id in ids):
                raise ValueError(f"Invalid or duplicate AICE record ID on line {line_number}")
            if records:
                ids.add(record_id)
                parent = record.get("parent_id", "")
                if parent and parent not in nodes:
                    raise ValueError(f"Missing AICE parent on line {line_number}")
                if record["type"] == "message" and not isinstance(record.get("message"), dict):
                    raise ValueError(f"Missing AICE message on line {line_number}")
                if record["type"] != "leaf":
                    nodes.add(record_id)
            records.append(record)
    if not records:
        raise ValueError("AICE session has no complete v3 header")
    return records


def _tool_group(record: dict[str, Any], nodes: dict[str, dict[str, Any]], call_id: Any) -> str:
    """Resolve the nearest assistant on this result's own branch, not by raw ID."""
    if not isinstance(call_id, str):
        return ""
    parent = record.get("parent_id", "")
    while parent:
        ancestor = nodes[parent]
        if ancestor["type"] != "message":
            break
        message = ancestor["message"]
        if message.get("role") == "assistant":
            return parent
        if message.get("role") != "toolResult" or message.get("tool_call_id") == call_id:
            break
        parent = ancestor.get("parent_id", "")
    return ""


def _content_text(content: Any, content_type: str) -> str:
    if not isinstance(content, list):
        return ""
    return "".join(
        part.get("text", "")
        for part in content
        if isinstance(part, dict)
        and part.get("type") == content_type
        and isinstance(part.get("text"), str)
    )


def _atif_tool_calls(content: Any) -> list[ToolCall]:
    if not isinstance(content, list):
        return []
    calls: list[ToolCall] = []
    for part in content:
        if not isinstance(part, dict) or part.get("type") != "tool_call":
            continue
        call = part.get("tool_call")
        if not isinstance(call, dict):
            continue
        call_id = call.get("id")
        name = call.get("name")
        if not isinstance(call_id, str) or not isinstance(name, str):
            continue
        calls.append(
            ToolCall(
                tool_call_id=call_id,
                function_name=name,
                arguments=_atif_arguments(call.get("arguments")),
            )
        )
    return calls


def _atif_arguments(arguments: Any) -> dict[str, Any]:
    if arguments is None or arguments == "":
        return {}
    if isinstance(arguments, dict):
        return arguments
    if isinstance(arguments, str):
        try:
            decoded = json.loads(arguments)
        except json.JSONDecodeError:
            return {"_raw": arguments}
        if isinstance(decoded, dict):
            return decoded
    return {"_raw": arguments}


def _atif_usage(usage: dict[str, Any]) -> tuple[int, int, int, float | None]:
    input_tokens = _nonnegative_int(usage.get("input_tokens"))
    cache_read_tokens = _nonnegative_int(usage.get("cache_read_tokens"))
    cache_write_tokens = _nonnegative_int(usage.get("cache_write_tokens"))
    output_tokens = _nonnegative_int(usage.get("output_tokens"))
    # Harbor defines prompt_tokens as the complete input, including cache
    # buckets; AICE input_tokens already excludes both cache buckets.
    prompt_tokens = input_tokens + cache_read_tokens + cache_write_tokens

    cost_usd: float | None = None
    cost = usage.get("cost")
    if isinstance(cost, dict):
        total = cost.get("total")
        if isinstance(total, (int, float)) and not isinstance(total, bool):
            cost_usd = float(total)
    return prompt_tokens, output_tokens, cache_read_tokens, cost_usd


def _nonnegative_int(value: Any) -> int:
    if isinstance(value, int) and not isinstance(value, bool) and value >= 0:
        return value
    return 0


def _atif_timestamp(value: Any) -> str | None:
    if not isinstance(value, (int, float)) or isinstance(value, bool) or value <= 0:
        return None
    try:
        timestamp = datetime.fromtimestamp(value / 1000, tz=timezone.utc)
    except (OSError, OverflowError, ValueError):
        return None
    return timestamp.isoformat().replace("+00:00", "Z")
