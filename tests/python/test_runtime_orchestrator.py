"""Unit tests for explicit runtime orchestrator."""

import os
import tempfile
import unittest

from _bootstrap import ensure_pythonpath
from _fixtures import memory_pressure_metrics, oom_logs

ensure_pythonpath()


class TestRuntimeOrchestrator(unittest.TestCase):
    """Validate explicit planner/tool/reasoning workflow."""

    def test_detects_memory_pressure(self):
        from sre_agent.runtime.config import RuntimeConfig
        from sre_agent.runtime.haystack_runtime import is_haystack_available
        from sre_agent.runtime.orchestrator import Orchestrator

        config = RuntimeConfig(
            reasoning_enabled=True,
            context_enabled=False,
            llm_enabled=False,
            memory_max_events=100,
        )
        orchestrator = Orchestrator(config)

        metrics = memory_pressure_metrics()
        logs = oom_logs()

        result = orchestrator.analyze("node-a", metrics, logs)
        self.assertTrue(result.issue_detected)
        self.assertEqual(result.issue_type, "memory_pressure")
        self.assertEqual(result.severity, "critical")
        self.assertIn("execution_trace", result.metadata)
        runtime_backend = result.metadata.get("runtime_backend")
        expected = "haystack" if is_haystack_available() else "native"
        self.assertEqual(runtime_backend, expected)

    def test_planner_adds_context_lookup_when_enabled(self):
        from sre_agent.runtime.config import RuntimeConfig
        from sre_agent.runtime.planner import Planner
        from sre_agent.runtime.types import TelemetryEnvelope

        config = RuntimeConfig(context_enabled=True)
        planner = Planner(config)
        env = TelemetryEnvelope(
            node_name="node-b",
            metrics=[{"name": "system.cpu.usage", "value": 88.0}],
            logs=[{"level": "warning", "message": "latency rising"}],
            request_id="req-1",
        )
        plan = planner.build_plan(env)
        tools = [step.tool_name for step in plan.steps if step.kind == "tool"]
        self.assertIn("context_lookup", tools)

    def test_context_lookup_reads_local_docs(self):
        from sre_agent.runtime.config import RuntimeConfig
        from sre_agent.runtime.orchestrator import Orchestrator

        with tempfile.TemporaryDirectory() as tmp:
            doc = os.path.join(tmp, "runbook.md")
            with open(doc, "w", encoding="utf-8") as handle:
                handle.write("If memory usage is high, inspect OOM events and cap runaway jobs.")

            config = RuntimeConfig(
                reasoning_enabled=True,
                context_enabled=True,
                context_paths=[doc],
                llm_enabled=False,
                memory_max_events=50,
            )
            orchestrator = Orchestrator(config)
            result = orchestrator.analyze(
                "node-c",
                memory_pressure_metrics(value=93.0),
                oom_logs(message="OOM detected"),
            )

            trace = result.metadata.get("execution_trace", [])
            step_names = [item.get("tool_name") for item in trace]
            self.assertIn("context_lookup", step_names)

    def test_haystack_backend_strict_mode(self):
        from sre_agent.runtime.config import RuntimeConfig
        from sre_agent.runtime.haystack_runtime import is_haystack_available
        from sre_agent.runtime.orchestrator import Orchestrator

        config = RuntimeConfig(
            reasoning_enabled=True,
            context_enabled=False,
            llm_enabled=False,
            runtime_backend="haystack",
            runtime_fail_open=False,
        )

        if is_haystack_available():
            orchestrator = Orchestrator(config)
            result = orchestrator.analyze("node-strict", [], [])
            self.assertEqual(result.metadata.get("runtime_backend"), "haystack")
            return

        with self.assertRaises(RuntimeError):
            Orchestrator(config)


if __name__ == "__main__":
    unittest.main()
