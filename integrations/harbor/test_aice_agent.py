"""Offline converter tests; small Harbor stand-ins avoid installing dependencies."""

import importlib.util
import json
import logging
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch


class Model:
    def __init__(self, **fields):
        self.__dict__.update(fields)


def load_adapter():
    modules = {}
    exports = {
        "harbor.agents.installed.base": {
            "BaseInstalledAgent": type("BaseInstalledAgent", (), {}),
            "NonZeroAgentExitCodeError": type("NonZeroAgentExitCodeError", (Exception,), {}),
            "with_prompt_template": lambda function: function,
        },
        "harbor.environments.base": {"BaseEnvironment": Model},
        "harbor.models.agent.context": {"AgentContext": Model},
        "harbor.models.trajectories": {name: Model for name in (
            "Agent", "FinalMetrics", "Metrics", "ObservationResult", "Step", "ToolCall", "Trajectory",
        )},
        "harbor.agents.capabilities": {"AgentCapabilities": Model},
    }
    for name, fields in exports.items():
        module = types.ModuleType(name)
        module.__dict__.update(fields)
        modules[name] = module
    spec = importlib.util.spec_from_file_location("aice_adapter_offline", Path(__file__).with_name("aice_agent.py"))
    adapter = importlib.util.module_from_spec(spec)
    with patch.dict(sys.modules, modules):
        spec.loader.exec_module(adapter)
    return adapter


ADAPTER = load_adapter()
HEADER = {"type": "session", "version": 3, "id": "session"}


def message(node_id, parent, role, text="", **fields):
    return {"type": "message", "id": node_id, "parent_id": parent, "created_at": 1000,
            "message": {"role": role, "content": [{"type": "text", "text": text}], **fields}}


def assistant(node_id, parent, call_id="call", cost=0.1):
    record = message(node_id, parent, "assistant", "working", usage={
        "input_tokens": 10, "cache_read_tokens": 2, "cache_write_tokens": 3,
        "output_tokens": 4, "cost": {"total": cost},
    })
    record["message"]["content"].append({"type": "tool_call", "tool_call": {
        "id": call_id, "name": "write", "arguments": {"path": "file.txt"},
    }})
    return record


class SessionConversionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "session.jsonl"
        self.agent = ADAPTER.AiceAgent.__new__(ADAPTER.AiceAgent)
        self.agent.logger = logging.getLogger(__name__)
        self.agent.model_name = "test/model"
        self.agent.version = lambda: "test-build"

    def convert(self, records, tail=""):
        source = "".join(json.dumps(record) + "\n" for record in records) + tail
        self.path.write_text(source)
        trajectory = self.agent._session_to_trajectory(self.path)
        self.assertEqual(self.path.read_text(), source)
        return trajectory

    def test_v2_and_missing_header_are_explicitly_rejected_without_rewrite(self):
        for records in ([{**HEADER, "version": 2}], [message("u", "", "user")]):
            self.path.write_text("".join(json.dumps(record) + "\n" for record in records))
            before = self.path.read_bytes()
            with self.assertRaisesRegex(ValueError, "requires Session format v3"):
                self.agent._session_to_trajectory(self.path)
            self.assertEqual(self.path.read_bytes(), before)

    def test_pending_group_and_incomplete_tail_keep_completed_messages(self):
        trajectory = self.convert([HEADER, message("u", "", "user", "request"), assistant("a", "u")], '{"type":')
        self.assertEqual(len(trajectory.steps), 2)
        self.assertEqual(trajectory.steps[1].tool_calls[0].tool_call_id, "call")
        self.assertFalse(hasattr(trajectory.steps[1], "observation"))
        self.assertEqual(trajectory.final_metrics.total_prompt_tokens, 15)

    def test_unknown_recovery_result_is_preserved(self):
        trajectory = self.convert([
            HEADER, message("u", "", "user"), assistant("a", "u"),
            message("r", "a", "toolResult", "Execution result unknown; inspect before retrying", tool_call_id="call", is_error=True),
        ])
        result = trajectory.steps[1].observation["results"][0]
        self.assertEqual(result.source_call_id, "call")
        self.assertIn("unknown", result.content)

    def test_repeated_call_ids_are_matched_by_parent_group_across_branches(self):
        trajectory = self.convert([
            HEADER, message("u", "", "user"), assistant("a1", "u"),
            message("r1", "a1", "toolResult", "first", tool_call_id="call"),
            assistant("a2", "r1"),
            message("r2", "a2", "toolResult", "second", tool_call_id="call"),
            {"type": "leaf", "id": "l", "parent_id": "r2", "target_id": "r1"},
            assistant("a3", "r1"),
            message("r3", "a3", "toolResult", "branch", tool_call_id="call"),
        ])
        observations = [step.observation["results"][0].content for step in trajectory.steps[1:]]
        self.assertEqual(observations, ["first", "second", "branch"])
        self.assertEqual(trajectory.final_metrics.total_completion_tokens, 12)
        self.assertAlmostEqual(trajectory.final_metrics.total_cost_usd, 0.3)

    def test_result_cannot_match_same_id_on_unrelated_branch(self):
        with self.assertRaisesRegex(ValueError, "Unmatched AICE tool result"):
            self.convert([
                HEADER, message("u", "", "user"), assistant("a", "u"),
                message("r", "u", "toolResult", "wrong branch", tool_call_id="call"),
            ])

    def test_multiple_results_share_only_their_current_group(self):
        call = assistant("a", "u")
        call["message"]["content"].append({"type": "tool_call", "tool_call": {"id": "other", "name": "read", "arguments": {}}})
        trajectory = self.convert([
            HEADER, message("u", "", "user"), call,
            message("r1", "a", "toolResult", "first", tool_call_id="call"),
            message("r2", "r1", "toolResult", "other", tool_call_id="other"),
        ])
        self.assertEqual([r.content for r in trajectory.steps[1].observation["results"]], ["first", "other"])

    def test_compaction_usage_is_counted_once_and_source_audit_is_retained(self):
        trajectory = self.convert([
            HEADER, message("u", "", "user", "original request"), assistant("a", "u"),
            message("r", "a", "toolResult", "done", tool_call_id="call"),
            {"type": "compaction", "id": "c", "parent_id": "r", "created_at": 1000,
             "summary": "summary", "usage": {"input_tokens": 7, "output_tokens": 2, "cost": {"total": 0.05}}},
        ])
        self.assertEqual([step.message for step in trajectory.steps], ["original request", "working", "[Context compaction]\nsummary"])
        self.assertEqual(trajectory.final_metrics.total_prompt_tokens, 22)
        self.assertEqual(trajectory.final_metrics.total_completion_tokens, 6)
        self.assertEqual(trajectory.final_metrics.total_cached_tokens, 2)
        self.assertAlmostEqual(trajectory.final_metrics.total_cost_usd, 0.15)
        self.assertEqual(sum(step.metrics.prompt_tokens for step in trajectory.steps if hasattr(step, "metrics")), 22)

    def test_header_id_is_independent_of_message_record_ids(self):
        trajectory = self.convert([
            HEADER, message("session", "", "user", "request"),
            assistant("a", "session"),
        ])
        self.assertEqual(trajectory.session_id, "session")
        self.assertEqual(len(trajectory.steps), 2)
        self.assertEqual(trajectory.steps[0].message, "request")

    def test_duplicate_record_cannot_double_count_usage(self):
        with self.assertRaisesRegex(ValueError, "duplicate AICE record ID"):
            self.convert([HEADER, message("u", "", "user"), assistant("a", "u"), assistant("a", "u")])


if __name__ == "__main__":
    unittest.main()
