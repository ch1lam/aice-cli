"""Harbor installed-agent adapter for AICE."""

from __future__ import annotations

import re
import shlex
from pathlib import Path
from typing import Any, override

from harbor.agents.installed.base import (
    BaseInstalledAgent,
    NonZeroAgentExitCodeError,
    with_prompt_template,
)
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

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

    if AgentCapabilities is not None:
        capabilities = AgentCapabilities()

    def __init__(
        self,
        logs_dir: Path,
        *args: Any,
        version: str | None = None,
        **kwargs: Any,
    ) -> None:
        super().__init__(logs_dir, *args, version=version, **kwargs)

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
            environment, ("curl", "ca_certificates", "ripgrep", "tar")
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
        command = (
            "aice --workspace . --print --yolo --approve -- "
            f"{escaped_instruction} 2>&1 | tee {log_file}"
        )

        exit_code = 0
        error: str | None = None
        try:
            await self.exec_as_agent(
                environment,
                command=command,
                env=self._build_env(),
            )
        except NonZeroAgentExitCodeError as exc:
            error = str(exc)
            match = _EXIT_CODE_RE.search(error)
            exit_code = int(match.group(1)) if match else 1
            raise
        finally:
            metadata = dict(context.metadata or {})
            metadata["exit_code"] = exit_code
            if error is not None:
                metadata["error"] = error
            context.metadata = metadata
