#!/usr/bin/env python3
"""Deterministic checks for the evaluation report renderer."""

import importlib.util
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("render_report.py")
SPEC = importlib.util.spec_from_file_location("render_report", SCRIPT)
RENDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RENDER)


class RenderReportTest(unittest.TestCase):
    def test_case_tokens_come_from_case_aggregate_total_tokens(self):
        case = {"aggregate": {"total_tokens": 1234, "mean_tokens": 99}}
        self.assertEqual(RENDER.case_token_count(case), 1234)
        self.assertIsNone(RENDER.case_token_count({"aggregate": {}}))

    def test_scorecard_respects_measured_dimensions(self):
        rows = RENDER.scorecard_rows(
            {
                "scorecard": {
                    "task_outcome": 0.75,
                    "diagnosis_quality": 0.0,
                    "measured": ["task_outcome"],
                }
            }
        )
        by_key = {key: value for key, _, value in rows}
        self.assertEqual(by_key["task_outcome"], 0.75)
        self.assertIsNone(by_key["diagnosis_quality"])

    def test_metric_support_distinguishes_unmeasured_zero(self):
        aggregate = {
            "root_cause_entity_f1": 0.0,
            "metric_support": {"diagnosis_quality": 0},
        }
        self.assertFalse(RENDER.metric_supported(aggregate, "diagnosis_quality"))
        aggregate["metric_support"]["diagnosis_quality"] = 2
        self.assertTrue(RENDER.metric_supported(aggregate, "diagnosis_quality"))

    def test_baseline_rows_are_split_by_unit(self):
        report = {
            "comparison": {
                "rows": [
                    {
                        "metric": "task_success_rate",
                        "baseline": 0.5,
                        "current": 0.6,
                        "units": "rate (0..1)",
                    },
                    {
                        "metric": "latency_p95_ms",
                        "baseline": 100,
                        "current": 120,
                        "units": "ms",
                    },
                    {
                        "metric": "mean_tokens",
                        "baseline": 900,
                        "current": 800,
                        "units": "tokens",
                    },
                    {"metric": "missing", "baseline": None, "current": 1, "units": "calls"},
                ]
            }
        }
        groups = dict(RENDER.baseline_groups(report))
        self.assertEqual(set(groups), {"rate", "ms", "tokens"})
        self.assertEqual(len(groups["rate"]), 1)

    def test_minimal_report_renders_explicit_empty_states(self):
        report = {
            "scorecard": {
                "task_outcome": 0.7,
                "overall_score": 0.7,
                "measured": ["task_outcome"],
            },
            "config": {"passing_threshold": 0.6},
            "gates": {"passed": True},
            "verdict": "pass",
            "aggregate": {},
            "cases": [
                {
                    "id": "case-a",
                    "task_type": "diagnose",
                    "incident_type": "memory",
                    "task_success_rate": 1.0,
                    "aggregate": {"total_tokens": 1200},
                },
                {
                    "id": "unmeasured-case",
                    "task_type": "verify",
                    "incident_type": "storage",
                    "aggregate": {},
                },
            ],
            "failure_modes": {},
        }
        with tempfile.TemporaryDirectory() as directory:
            RENDER.render_scorecard(report, directory)
            RENDER.render_incident_type_success(report, directory)
            RENDER.render_rca(report, directory)
            RENDER.render_cost_vs_success(report, directory)
            RENDER.render_stability(report, directory)
            expected = {
                "scorecard.png",
                "incident_type_success.png",
                "rca.png",
                "cost_vs_success.png",
                "stability.png",
            }
            self.assertEqual({path.name for path in Path(directory).glob("*.png")}, expected)
            for name in expected:
                self.assertGreater((Path(directory) / name).stat().st_size, 0)


if __name__ == "__main__":
    unittest.main()
