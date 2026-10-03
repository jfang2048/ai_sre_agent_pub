#!/usr/bin/env python3
"""Offline tests for evidence validation; no Go processes or services are run."""

import contextlib
import copy
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import validate_effectiveness as validation


def monitoring_events():
    events = []
    for scenario, truth in validation.SCENARIOS.items():
        name = validation.ROOT_TEST + "/" + scenario
        findings = ["queue_pressure", "gpu_memory_pressure"] if truth in {"anomaly", "replay"} else []
        item = {"scenario": scenario, "ground_truth": truth, "expected_findings": findings,
                "observed_findings": findings, "elapsed_ms": 5.0}
        events += [{"Action": "output", "Test": name, "Output": "    test.go:1: " + validation.EVIDENCE_MARKER + json.dumps(item) + "\n"},
                   {"Action": "pass", "Test": name}]
    events.append({"Action": "pass", "Test": validation.ROOT_TEST})
    events.append({"Action": "pass"})
    return events


def raw_log(events):
    return "\n".join(json.dumps(event) for event in events)


def valid_report():
    provenance = {key: "a" * 64 for key in ("case_definitions_sha256", "incident_inputs_sha256", "scoring_config_sha256", "knowledge_corpus_sha256", "evaluator_sha256", "regression_policy_sha256")}
    provenance.update(schema_version="evaluation-inputs/v1", evaluator_source="repository-source")
    return {"schema_version": "system-performance/v2", "verdict": "PASS", "gates": {"passed": True, "checks": [{"name": "case_pass", "passed": True}]},
            "scorecard": {"overall_score": 0.75},
            "environment": {"scope": "benchmark", "trials_per_case": 1, "seed": 42, "runtime_mode": "legacy_deterministic", "provenance": provenance},
            "cases": [{"id": "case-a", "passed": True, "trials": 1, "task_success_trials": 1, "task_success_rate": 1, "trials_detail": [{"task_success": True}]}]}


class MonitoringValidationTests(unittest.TestCase):
    def test_required_stages_and_confusion_matrix(self):
        result = validation.parse_monitoring_log(raw_log(monitoring_events()))
        self.assertEqual(len(result["stages"]), 8)
        observed = result["baselines"]["observed"]
        self.assertEqual(observed["labeled_stages"], 5)
        self.assertEqual((observed["true_positive"], observed["false_positive"], observed["true_negative"], observed["false_negative"]), (1, 0, 4, 0))
        alert = result["baselines"]["always_alert"]
        self.assertEqual(alert["precision"], 0.2)
        self.assertEqual(alert["false_positive_rate"], 1)
        quiet = result["baselines"]["always_quiet"]
        self.assertIsNone(quiet["precision"])
        self.assertEqual(quiet["recall"], 0)
        self.assertEqual(quiet["false_negative"], 1)

    def test_missing_skipped_or_failed_root_is_rejected(self):
        for action in (None, "skip", "fail"):
            with self.subTest(action=action):
                events = [event for event in monitoring_events() if event.get("Test") != validation.ROOT_TEST]
                if action:
                    events.append({"Test": validation.ROOT_TEST, "Action": action})
                with self.assertRaisesRegex(validation.EvidenceError, "required tests"):
                    validation.parse_monitoring_log(raw_log(events))

    def test_missing_or_skipped_subtest_is_rejected(self):
        for action in (None, "skip"):
            with self.subTest(action=action):
                name = validation.ROOT_TEST + "/stale"
                events = [event for event in monitoring_events() if not (event.get("Test") == name and event["Action"] == "pass")]
                if action:
                    events.append({"Test": name, "Action": action})
                with self.assertRaises(validation.EvidenceError):
                    validation.parse_monitoring_log(raw_log(events))

    def test_missing_or_duplicate_evidence_is_rejected(self):
        events = monitoring_events()
        with self.assertRaisesRegex(validation.EvidenceError, "missing monitoring evidence"):
            validation.parse_monitoring_log(raw_log(events[1:]))
        with self.assertRaisesRegex(validation.EvidenceError, "duplicate"):
            validation.parse_monitoring_log(raw_log(events + [events[0]]))

    def test_evidence_must_be_from_its_subtest(self):
        events = monitoring_events()
        events[0]["Test"] = validation.ROOT_TEST
        with self.assertRaisesRegex(validation.EvidenceError, "attached"):
            validation.parse_monitoring_log(raw_log(events))

    def test_changed_ground_truth_mismatch_and_missing_measurement_rejected(self):
        for edit, error in (({"ground_truth": "anomaly"}, "ground truth"),
                            ({"observed_findings": ["unwanted_alert"]}, "differ from expected"),
                            ({"expected_findings": None}, "expected_findings"),
                            ({"elapsed_ms": None}, "elapsed_ms"),
                            ({"elapsed_ms": -1}, "elapsed_ms"),
                            ({"elapsed_ms": True}, "elapsed_ms")):
            with self.subTest(edit=edit):
                events = monitoring_events()
                item = json.loads(events[0]["Output"].split(validation.EVIDENCE_MARKER)[1])
                item.update(edit)
                events[0]["Output"] = validation.EVIDENCE_MARKER + json.dumps(item) + "\n"
                with self.assertRaisesRegex(validation.EvidenceError, error):
                    validation.parse_monitoring_log(raw_log(events))

    def test_json_output_chunks_are_joined(self):
        events = monitoring_events()
        value = events[0]["Output"]
        events[0]["Output"] = value[:30]
        events.insert(1, {"Action": "output", "Test": events[0]["Test"], "Output": value[30:]})
        self.assertEqual(len(validation.parse_monitoring_log(raw_log(events))["stages"]), 8)

    def test_malformed_and_nonfinite_json_fail_closed(self):
        for raw in ("not json", "[]", '{"Action":NaN}'):
            with self.subTest(raw=raw), self.assertRaises(validation.EvidenceError):
                validation.parse_monitoring_log(raw)

    def test_unmeasured_baseline_denominators_remain_null(self):
        row = validation.confusion_matrix([{"ground_truth": "unavailable", "observed_findings": []}])
        self.assertEqual(row["labeled_stages"], 0)
        self.assertIsNone(row["precision"])
        self.assertIsNone(row["recall"])
        self.assertIsNone(row["false_positive_rate"])


class WorkflowValidationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "report.json"

    def write(self, report):
        self.path.write_text(json.dumps(report), encoding="utf-8")

    def test_valid_report_and_expected_coverage(self):
        self.write(valid_report())
        self.assertEqual(validation.validate_workflow_report(self.path, 1, ["case-a"])["verdict"], "PASS")
        with self.assertRaisesRegex(validation.EvidenceError, "case set"):
            validation.validate_workflow_report(self.path, 1, ["case-a", "case-b"])

    def test_missing_or_invalid_report(self):
        with self.assertRaisesRegex(validation.EvidenceError, "missing or invalid"):
            validation.validate_workflow_report(self.path, 1)
        for text in ("no", "[]", "{}", '{"schema_version":NaN}'):
            self.path.write_text(text, encoding="utf-8")
            with self.subTest(text=text), self.assertRaises(validation.EvidenceError):
                validation.validate_workflow_report(self.path, 1)

    def test_fail_verdict_gates_and_case_fail_closed(self):
        for field in ("verdict", "gate", "case"):
            report = valid_report()
            if field == "verdict":
                report["verdict"] = "FAIL"
            elif field == "gate":
                report["gates"]["passed"] = False
                report["gates"]["checks"][0]["passed"] = False
            else:
                report["cases"][0]["passed"] = False
            self.write(report)
            with self.subTest(field=field), self.assertRaisesRegex(validation.EvidenceError, "gate failed"):
                validation.validate_workflow_report(self.path, 1)

    def test_runtime_trials_and_missing_detail_are_rejected(self):
        for field in ("mode", "trials", "detail", "cases"):
            report = valid_report()
            if field == "mode":
                report["environment"]["runtime_mode"] = "full_adaptive"
            elif field == "trials":
                report["environment"]["trials_per_case"] = 5
            elif field == "detail":
                del report["cases"][0]["trials_detail"]
            else:
                report["cases"] = []
            self.write(report)
            with self.subTest(field=field), self.assertRaises(validation.EvidenceError):
                validation.validate_workflow_report(self.path, 1)

    def test_failed_report_retains_measurements(self):
        report = valid_report()
        report["verdict"] = "FAIL"
        report["cases"][0].update(passed=False, failures=["root cause incorrect"])
        self.write(report)
        with self.assertRaises(validation.WorkflowGateError) as caught:
            validation.validate_workflow_report(self.path, 1)
        summary = validation.workflow_summary(caught.exception.report)
        self.assertEqual(summary["cases_passed"], 0)
        self.assertEqual(summary["overall_score"], 0.75)
        self.assertEqual(summary["failed_cases"][0]["failures"], ["root cause incorrect"])

    def test_trial_measurements_cannot_be_replaced_by_summary_flags(self):
        for detail in ({}, None, {"task_success": False}, {"task_success": 1}):
            report = valid_report()
            report["cases"][0]["trials_detail"] = [detail]
            self.write(report)
            with self.subTest(detail=detail), self.assertRaises(validation.EvidenceError):
                validation.validate_workflow_report(self.path, 1)

    def test_provenance_and_gate_evidence_are_required(self):
        for field in ("provenance", "hash", "gate_checks", "gate_mismatch"):
            report = valid_report()
            if field == "provenance":
                del report["environment"]["provenance"]
            elif field == "hash":
                report["environment"]["provenance"]["evaluator_sha256"] = "invalid"
            elif field == "gate_checks":
                del report["gates"]["checks"]
            else:
                report["gates"]["checks"][0]["passed"] = False
            self.write(report)
            with self.subTest(field=field), self.assertRaises(validation.EvidenceError):
                validation.validate_workflow_report(self.path, 1)

    def test_invalid_measurements_cannot_be_presented_as_failed_experiment(self):
        for score in (None, True, -0.1, 1.1):
            report = valid_report()
            report["scorecard"]["overall_score"] = score
            self.write(report)
            with self.subTest(score=score), self.assertRaisesRegex(validation.EvidenceError, "valid overall score"):
                validation.validate_workflow_report(self.path, 1)


class OrchestratorTests(unittest.TestCase):
    def test_environment_strips_secrets_without_recording_values(self):
        source = {"PATH": "/bin", "GOCACHE": "/tmp/go-cache", "SRE_AGENT_LLM_API_KEY": "do-not-record", "SRE_OTHER": "private"}
        env, removed, forced = validation.fixture_environment(source, Path("/tmp/evidence"))
        self.assertEqual(env["PATH"], source["PATH"])
        self.assertEqual(env["GOCACHE"], source["GOCACHE"])
        self.assertNotIn("SRE_AGENT_LLM_API_KEY", env)
        self.assertEqual(removed, ["SRE_AGENT_LLM_API_KEY", "SRE_OTHER"])
        self.assertNotIn("do-not-record", json.dumps({"removed": removed, "forced": forced}))
        self.assertEqual(env["SRE_AGENT_WORKFLOW_INSIGHTS_ENABLED"], "false")

    def test_existing_output_even_empty_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(FileExistsError):
                validation.reserve_output(directory)

    def fake_monitoring(self, returncode=0):
        def run(name, command, repo_root, output, env, timeout):
            (output / (name + ".stdout.log")).write_text(raw_log(monitoring_events()), encoding="utf-8")
            (output / (name + ".stderr.log")).write_text("", encoding="utf-8")
            return {"name": name, "command": command, "returncode": returncode, "duration_seconds": 0.01,
                    "stdout": name + ".stdout.log", "stderr": name + ".stderr.log"}
        return run

    def test_monitoring_only_is_explicit_and_artifacts_are_hashed(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "new"
            with patch.object(validation, "run_command", self.fake_monitoring()), patch.object(validation, "source_identity", return_value={"commit": "test", "worktree_dirty": False}), contextlib.redirect_stdout(io.StringIO()):
                result = validation.main(["--monitoring-only", "--output-dir", str(output)])
            self.assertEqual(result, 0)
            manifest = json.loads((output / "manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(manifest["verdict"], "PASS_MONITORING_ONLY")
            self.assertEqual(len(manifest["commands"]), 1)
            self.assertEqual(len(manifest["artifacts"]["monitoring.stdout.log"]["sha256"]), 64)
            self.assertIn("未测量", (output / "summary.md").read_text(encoding="utf-8"))

    def test_command_failure_cannot_be_overridden_by_passing_output(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(validation, "run_command", self.fake_monitoring(2)), patch.object(validation, "source_identity", return_value={"commit": "test", "worktree_dirty": False}), contextlib.redirect_stdout(io.StringIO()):
            output = Path(directory) / "new"
            self.assertEqual(validation.main(["--monitoring-only", "--output-dir", str(output)]), 1)
            self.assertEqual(json.loads((output / "manifest.json").read_text(encoding="utf-8"))["verdict"], "FAIL")

    def test_full_command_preserves_failed_workflow_evidence(self):
        def fake_run(name, command, repo_root, output, env, timeout):
            result = self.fake_monitoring()(name, command, repo_root, output, env, timeout)
            if name == "workflow":
                fixture = json.loads((repo_root / "eval_data/system_perf_cases_v2.json").read_text())
                report = valid_report()
                template = report["cases"][0]
                report["cases"] = [dict(copy.deepcopy(template), id=c["id"]) for c in fixture["cases"] if "benchmark" in c["suites"]]
                report["cases"][0].update(passed=False, task_success_trials=0, task_success_rate=0,
                                          trials_detail=[{"task_success": False}], failures=["wrong root cause"])
                report["verdict"] = "FAIL"
                report["gates"]["passed"] = report["gates"]["checks"][0]["passed"] = False
                (output / "workflow").mkdir()
                (output / "workflow/report.json").write_text(json.dumps(report))
                result["returncode"] = 1
            return result

        with tempfile.TemporaryDirectory() as directory, patch.object(validation, "run_command", fake_run), contextlib.redirect_stdout(io.StringIO()):
            output = Path(directory) / "new"
            self.assertEqual(validation.main(["--output-dir", str(output)]), 1)
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(manifest["verdict"], "FAIL")
            self.assertEqual(manifest["workflow"]["cases_passed"], manifest["workflow"]["cases_run"] - 1)
            self.assertIn("wrong root cause", (output / "summary.md").read_text())
            self.assertIn("workflow/report.json", manifest["artifacts"])

    def test_timeout_is_bounded_and_recorded(self):
        with tempfile.TemporaryDirectory() as directory:
            result = validation.run_command("short", [os.sys.executable, "-c", "import time; time.sleep(60)"], Path(directory), Path(directory), os.environ.copy(), 0.02)
            self.assertEqual(result["returncode"], 124)
            self.assertTrue(result["timed_out"])
            self.assertLess(result["duration_seconds"], 5)


if __name__ == "__main__":
    unittest.main()
