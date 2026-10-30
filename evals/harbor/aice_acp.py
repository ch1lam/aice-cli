"""Harbor's generic ACP agent running a locally built AICE binary.

Harbor's binary distribution downloads HTTPS archives, so this subclass
uploads the binary instead, and installs only what the ACP runner needs.
Everything else, including the ATIF trajectory, is Harbor's AcpAgent.
"""

from __future__ import annotations

import shlex
from pathlib import Path, PurePosixPath
from typing import Any

from harbor.agents.installed.acp import AcpAgent, AcpBinaryTarget

_ARGS = [
    "acp", "--workspace", ".", "--approve",
    "--session", "/logs/agent/aice-session.jsonl",
]
_PLATFORMS = (("linux-x86_64", "amd64"), ("linux-aarch64", "arm64"))


class AiceAcpAgent(AcpAgent):
    """Run `aice acp` from binary_dir/aice-linux-{amd64,arm64}.

    Harbor's `-m provider/model` selects the AICE provider and model, so the
    ACP model option the runner sets matches the effective model.
    """

    def __init__(self, *args: Any, binary_dir: str | None = None, **kwargs: Any):
        self._binary_dir = Path(binary_dir) if binary_dir else Path(__file__).with_name("bin")
        env = {"AICE_NO_UPDATE_CHECK": "1"}
        model = kwargs.get("model_name")
        if model:
            provider, separator, name = model.partition("/")
            if not separator or not provider or not name:
                raise ValueError(f"model must be provider/model, got {model!r}")
            env |= {"AICE_PROVIDER": provider, "AICE_MODEL": name}
        entry = {
            "id": "aice",
            "name": "AICE",
            "version": "local",
            "description": "AICE local build",
            "distribution": {
                "binary": {
                    platform: {
                        "archive": f"https://local.invalid/aice-linux-{arch}",
                        "cmd": "aice",
                        "args": _ARGS,
                        "env": env,
                    }
                    for platform, arch in _PLATFORMS
                }
            },
        }
        super().__init__(*args, registry_entry=entry, **kwargs)

    def _build_dependencies_command(self, kind: str) -> str:
        """Install the runner's Python venv and CA certificates only when
        missing, without recommended packages. Harbor's default installs
        python3-pip with its recommended compiler toolchain on every trial,
        which can exceed the agent setup timeout on a slow package mirror.
        The ACP client needs Python 3.10 or later.
        """
        venv = shlex.quote(self._RUNNER_VENV_PATH)
        return f"""
set -eu
has_certs() {{
  for file in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem; do
    [ -s "$file" ] && return 0
  done
  return 1
}}
has_python() {{
  python3 -c 'import sys, ensurepip, venv; sys.exit(sys.version_info < (3, 10))' >/dev/null 2>&1
}}
if ! has_python || ! has_certs; then
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends python3 python3-venv ca-certificates
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache python3 ca-certificates
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y python3 ca-certificates
  elif command -v yum >/dev/null 2>&1; then
    yum install -y python3 ca-certificates
  else
    echo "no package manager to install python3 and ca-certificates" >&2
    exit 1
  fi
fi
if ! has_python; then
  echo "Harbor's ACP client needs Python 3.10 or later with venv; this image provides $(python3 --version 2>&1)" >&2
  exit 1
fi
if ! {venv}/bin/python -c 'import acp' >/dev/null 2>&1; then
  rm -rf {venv}
  python3 -m venv {venv}
  {venv}/bin/pip install -q --disable-pip-version-check agent-client-protocol
fi
""".strip()

    async def _install_binary_target(self, environment, target: AcpBinaryTarget) -> None:
        source = self._binary_dir / PurePosixPath(target.archive).name
        if not source.is_file():
            raise FileNotFoundError(f"{source} is missing; build it as docs/evaluation.md describes")
        dist = PurePosixPath(self._BINARY_INSTALL_DIR) / "dist"
        binary = shlex.quote(str(dist / "aice"))
        await self.exec_as_root(environment, command=f"rm -rf {self._BINARY_INSTALL_DIR} && mkdir -p {dist}")
        await environment.upload_file(source_path=source, target_path=str(dist / "aice"))
        await self.exec_as_root(environment, command=f"chmod -R a+rX {self._BINARY_INSTALL_DIR} && chmod 755 {binary}")
