#!/usr/bin/env python3
"""Validate AICE's ACP messages against the schema pinned by go.mod.

The input is a transcript written by the ACP end-to-end test:

    ACP_TRANSCRIPT=/tmp/acp.jsonl go test ./internal/app -run TestACPServes
    python3 evals/acp/validate_schema.py /tmp/acp.jsonl

Lines start with "->" (client to AICE) or "<-" (AICE to client). Every
message AICE sends is checked against the definition its method selects;
responses are matched to the method of the request they answer. The schema
is the unstable variant of the SDK's pinned schema because AICE sends usage
updates. Requires the jsonschema package.
"""

import json
import pathlib
import subprocess
import sys

import jsonschema

SDK = "github.com/coder/acp-go-sdk"

# Responses AICE returns, by the method of the client request.
RESPONSES = {
    "initialize": "InitializeResponse",
    "authenticate": "AuthenticateResponse",
    "session/new": "NewSessionResponse",
    "session/load": "LoadSessionResponse",
    "session/resume": "ResumeSessionResponse",
    "session/prompt": "PromptResponse",
    "session/set_mode": "SetSessionModeResponse",
    "session/set_config_option": "SetSessionConfigOptionResponse",
}

# Requests and notifications AICE sends.
OUTBOUND = {
    "session/update": "SessionNotification",
    "session/request_permission": "RequestPermissionRequest",
}


def schema_path() -> pathlib.Path:
    root = pathlib.Path(__file__).resolve().parents[2]
    directory = subprocess.run(
        ["go", "list", "-m", "-f", "{{.Dir}}", SDK],
        cwd=root, check=True, capture_output=True, text=True,
    ).stdout.strip()
    return pathlib.Path(directory) / "schema" / "schema.unstable.json"


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    schema = json.loads(schema_path().read_text())
    defs = schema["$defs"]

    def validator(name: str) -> jsonschema.Draft202012Validator:
        return jsonschema.Draft202012Validator({"$ref": f"#/$defs/{name}", "$defs": defs})

    pending: dict[object, str] = {}
    checked = failures = 0
    for number, line in enumerate(pathlib.Path(sys.argv[1]).read_text().splitlines(), 1):
        direction, _, payload = line.partition(" ")
        message = json.loads(payload)
        if direction == "->":
            if "method" in message and "id" in message:
                pending[message["id"]] = message["method"]
            continue
        if "method" in message:
            name, value = OUTBOUND.get(message["method"]), message.get("params")
        elif "error" in message:
            name, value = "Error", message["error"]
            pending.pop(message.get("id"), None)
        else:
            name, value = RESPONSES.get(pending.pop(message.get("id"), "")), message.get("result")
        if name is None:
            print(f"line {number}: no schema for {payload[:120]}")
            failures += 1
            continue
        errors = sorted(validator(name).iter_errors(value), key=lambda e: list(e.path))
        for error in errors:
            print(f"line {number}: {name} {'/'.join(map(str, error.path))}: {error.message}")
        failures += bool(errors)
        checked += 1
    print(f"{checked} messages checked against {schema_path().parent.parent.name}, {failures} failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
