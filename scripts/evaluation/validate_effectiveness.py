#!/usr/bin/env python3
"""Run reproducible synthetic effectiveness experiments and keep auditable evidence.

The monitoring experiment traverses the collector, gRPC ingest and controller API.
The workflow experiment runs the existing deterministic v2 benchmark without
weakening its gates. Neither experiment establishes production effectiveness.
"""

import argparse
import datetime as dt
import hashlib
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import uuid


ROOT_TEST = "TestMonitoringEffectivenessPipeline"
EVIDENCE_MARKER = "EFFECTIVENESS_EVIDENCE "
SCENARIOS = {
    "healthy": "healthy",
    "fault_detected": "anomaly",
    "unrelated_model_isolation": "healthy",
    "recovery": "healthy",
    "counter_reset": "healthy",
    "scrape_failure": "unavailable",
    "restart_restore": "replay",
    "stale": "stale",
}
LIMITS = [
    "监测输入来自合成 exporter 和 GPU 遥测帧，未验证真实显卡、驱动或真实 vLLM 服务。",
    "工作流使用固定合成场景和 stub/mock 模型，未证明真实大模型的推理质量。",
    "监测阶段属于同一条时间序列，不是独立随机试验；重复工作流也是描述性重放。",
    "对照只检查是否发出告警，不代表与人工值班或其他监控产品进行了公平性能比较。",
    "未测生产误报率、真实发现时间、真实恢复时间（MTTR）或业务影响。",
]


class EvidenceError(ValueError):
    """A required piece of evidence is absent, inconsistent or failed."""


class WorkflowGateError(EvidenceError):
    """A complete report records a failed experiment; retain its measurements."""

    def __init__(self, message, report):
        super().__init__(message)
        self.report = report


def strict_json(text):
    def invalid_constant(value):
        raise EvidenceError("non-finite JSON constant: " + value)

    return json.loads(text, parse_constant=invalid_constant)


def finite_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def confusion_matrix(evidence, strategy="observed"):
    """Count only pre-labeled anomaly/healthy stages, never stale or unavailable."""
    if strategy not in {"observed", "always_alert", "always_quiet"}:
        raise ValueError("unknown strategy: " + strategy)
    result = {"true_positive": 0, "false_positive": 0, "true_negative": 0, "false_negative": 0}
    for item in evidence:
        if item["ground_truth"] not in {"anomaly", "healthy"}:
            continue
        actual = item["ground_truth"] == "anomaly"
        predicted = bool(item["observed_findings"]) if strategy == "observed" else strategy == "always_alert"
        key = ("true_positive" if actual else "false_positive") if predicted else ("false_negative" if actual else "true_negative")
        result[key] += 1
    tp, fp, tn, fn = (result[key] for key in ("true_positive", "false_positive", "true_negative", "false_negative"))
    result.update({
        "labeled_stages": tp + fp + tn + fn,
        "precision": tp / (tp + fp) if tp + fp else None,
        "recall": tp / (tp + fn) if tp + fn else None,
        "false_positive_rate": fp / (fp + tn) if fp + tn else None,
    })
    return result


def parse_monitoring_log(raw):
    """Require successful root/subtests AND one consistent evidence row per stage."""
    terminal = {}
    output = {}
    for line in raw.splitlines():
        if not line.strip():
            continue
        try:
            event = strict_json(line)
        except (ValueError, TypeError) as exc:
            raise EvidenceError("invalid go test JSON event") from exc
        if not isinstance(event, dict):
            raise EvidenceError("go test event is not an object")
        name = event.get("Test", "")
        action = event.get("Action")
        if not isinstance(name, str) or not isinstance(action, str):
            raise EvidenceError("invalid go test event name/action")
        if action in {"pass", "fail", "skip"}:
            terminal[name] = action
        if isinstance(event.get("Output"), str):
            output.setdefault(name, []).append(event["Output"])
    required = [ROOT_TEST] + [ROOT_TEST + "/" + name for name in SCENARIOS]
    missing = [name for name in required if terminal.get(name) != "pass"]
    failed = [name or "package" for name, action in terminal.items() if action in {"fail", "skip"}]
    if missing or failed:
        raise EvidenceError("missing/non-passing required tests: " + ", ".join(missing + failed))
    rows = {}
    for test_name, chunks in output.items():
        for line in "".join(chunks).splitlines():
            if EVIDENCE_MARKER not in line:
                continue
            try:
                item = strict_json(line.split(EVIDENCE_MARKER, 1)[1])
            except (ValueError, TypeError) as exc:
                raise EvidenceError("invalid monitoring evidence JSON") from exc
            if not isinstance(item, dict):
                raise EvidenceError("monitoring evidence is not an object")
            name = item.get("scenario")
            if not isinstance(name, str) or name not in SCENARIOS or name in rows:
                raise EvidenceError("unknown or duplicate monitoring scenario: " + str(name))
            if test_name != ROOT_TEST + "/" + name:
                raise EvidenceError("evidence is not attached to its required subtest: " + name)
            if item.get("ground_truth") != SCENARIOS[name]:
                raise EvidenceError("ground truth differs from the declared experiment: " + name)
            for key in ("expected_findings", "observed_findings"):
                values = item.get(key)
                if not isinstance(values, list) or any(not isinstance(v, str) or not v for v in values):
                    raise EvidenceError("missing/invalid " + key + ": " + name)
                if len(values) != len(set(values)):
                    raise EvidenceError("duplicate findings: " + name)
            if set(item["expected_findings"]) != set(item["observed_findings"]):
                raise EvidenceError("observed findings differ from expected: " + name)
            if SCENARIOS[name] == "healthy" and item["observed_findings"]:
                raise EvidenceError("healthy stage emitted findings: " + name)
            if SCENARIOS[name] == "anomaly" and not item["observed_findings"]:
                raise EvidenceError("anomaly stage emitted no findings: " + name)
            if not finite_number(item.get("elapsed_ms")) or item["elapsed_ms"] < 0:
                raise EvidenceError("missing/invalid elapsed_ms: " + name)
            rows[name] = item
    absent = sorted(set(SCENARIOS) - set(rows))
    if absent:
        raise EvidenceError("missing monitoring evidence: " + ", ".join(absent))
    ordered = [rows[name] for name in SCENARIOS]
    return {"stages": ordered, "baselines": {name: confusion_matrix(ordered, name) for name in ("observed", "always_alert", "always_quiet")}}


def validate_workflow_report(path, trials, expected_case_ids=None):
    try:
        report = strict_json(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise EvidenceError("missing or invalid workflow report.json") from exc
    if not isinstance(report, dict) or report.get("schema_version") != "system-performance/v2":
        raise EvidenceError("workflow report is not system-performance/v2")
    environment = report.get("environment", {})
    if not isinstance(environment, dict) or environment.get("scope") != "benchmark" or environment.get("trials_per_case") != trials:
        raise EvidenceError("workflow scope or trial count differs from requested experiment")
    if environment.get("runtime_mode") != "legacy_deterministic" or environment.get("seed") != 42:
        raise EvidenceError("workflow runtime mode or bootstrap seed differs from requested experiment")
    provenance = environment.get("provenance", {})
    hashes = ("case_definitions_sha256", "incident_inputs_sha256", "scoring_config_sha256",
              "knowledge_corpus_sha256", "evaluator_sha256", "regression_policy_sha256")
    if (not isinstance(provenance, dict) or provenance.get("schema_version") != "evaluation-inputs/v1"
            or provenance.get("evaluator_source") != "repository-source"
            or any(not isinstance(provenance.get(key), str) or not re.fullmatch(r"[0-9a-f]{64}", provenance[key]) for key in hashes)):
        raise EvidenceError("workflow report lacks supported input provenance")
    cases = report.get("cases")
    if not isinstance(cases, list) or not cases or any(not isinstance(c, dict) for c in cases):
        raise EvidenceError("workflow cases are missing")
    ids = [c.get("id") for c in cases]
    if any(not isinstance(i, str) or not i for i in ids) or len(set(ids)) != len(ids):
        raise EvidenceError("workflow case IDs are missing or duplicated")
    if expected_case_ids is not None and set(ids) != set(expected_case_ids):
        raise EvidenceError("workflow report does not cover the requested benchmark case set")
    if any(c.get("trials") != trials or not isinstance(c.get("trials_detail"), list) or len(c["trials_detail"]) != trials for c in cases):
        raise EvidenceError("workflow report lacks requested per-trial evidence")
    for case in cases:
        details = case["trials_detail"]
        if any(not isinstance(t, dict) or not isinstance(t.get("task_success"), bool) for t in details):
            raise EvidenceError("workflow report lacks per-trial task verdicts: " + case["id"])
        successes = sum(t["task_success"] for t in details)
        if (type(case.get("task_success_trials")) is not int or case["task_success_trials"] != successes
                or not finite_number(case.get("task_success_rate")) or not math.isclose(case["task_success_rate"], successes / trials)
                or (case.get("passed") is True and successes != trials)):
            raise EvidenceError("workflow trial outcomes disagree with case summary: " + case["id"])
    scorecard = report.get("scorecard", {})
    if not isinstance(scorecard, dict) or not finite_number(scorecard.get("overall_score")) or not 0 <= scorecard["overall_score"] <= 1:
        raise EvidenceError("workflow report lacks a valid overall score")
    if any(not isinstance(c.get("passed"), bool) for c in cases):
        raise EvidenceError("workflow report lacks per-case verdicts")
    failed = [c["id"] for c in cases if c.get("passed") is not True]
    gates = report.get("gates", {})
    if report.get("verdict") not in {"PASS", "FAIL"} or not isinstance(gates, dict) or not isinstance(gates.get("passed"), bool):
        raise EvidenceError("workflow report lacks a valid gate verdict")
    checks = gates.get("checks")
    if (not isinstance(checks, list) or not checks
            or any(not isinstance(c, dict) or not isinstance(c.get("passed"), bool) for c in checks)
            or gates["passed"] != all(c["passed"] for c in checks)):
        raise EvidenceError("workflow gate checks disagree with gate summary")
    if report["verdict"] != "PASS" or not gates["passed"] or failed:
        raise WorkflowGateError("workflow gate failed; verdict=" + report["verdict"] + "; failed cases=" + ", ".join(failed), report)
    return report


def workflow_summary(report):
    return {"verdict": report["verdict"], "cases_run": len(report["cases"]),
            "cases_passed": sum(c["passed"] for c in report["cases"]),
            "overall_score": report["scorecard"]["overall_score"],
            "failed_cases": [{"id": c["id"], "failures": c.get("failures", [])} for c in report["cases"] if not c["passed"]],
            "gates": report["gates"], "report": "workflow/report.json"}


def fixture_environment(source, output_dir):
    removed = sorted(key for key in source if key.startswith("SRE_"))
    env = {key: value for key, value in source.items() if key not in removed}
    forced = {
        "SRE_AGENT_WORKFLOW_RUNTIME_MODE": "legacy_deterministic",
        "SRE_AGENT_WORKFLOW_INSIGHTS_ENABLED": "false",
        "SRE_AGENT_LLM_ENABLED": "false",
        "SRE_AGENT_WORKFLOW_INSIGHTS_PROVIDER": "stub",
        "SRE_AGENT_WORKFLOW_DRY_RUN": "true",
        "SRE_AGENT_WORKFLOW_REQUIRE_APPROVAL": "true",
        "SRE_AGENT_WORKFLOW_ALLOW_PROFILING_EXEC": "false",
        "SRE_AGENT_WORKFLOW_ALLOW_REMEDIATION_EXEC": "false",
        "SRE_AGENT_ACTION_RUNNER_DRY_RUN": "true",
        "SRE_AGENT_ACTION_RUNNER_ALLOW_UNSAFE": "false",
        "SRE_AGENT_WORKFLOW_DATA_PATH": str(output_dir / "runtime"),
        "SRE_AGENT_WORKFLOW_STORE_PATH": str(output_dir / "runtime/workflow_runs.db"),
    }
    env.update(forced)
    return env, removed, forced


def reserve_output(path):
    """Creation is exclusive even when the supplied directory is empty."""
    path = Path(path).resolve()
    path.mkdir(parents=True, exist_ok=False)
    return path


def run_command(name, command, repo_root, output, env, timeout):
    stdout_path, stderr_path = output / (name + ".stdout.log"), output / (name + ".stderr.log")
    started = time.monotonic()
    result = {"name": name, "command": command, "timeout_seconds": timeout, "timed_out": False,
              "stdout": stdout_path.name, "stderr": stderr_path.name}
    print("Running " + name + "...", flush=True)
    with stdout_path.open("w", encoding="utf-8") as stdout, stderr_path.open("w", encoding="utf-8") as stderr:
        try:
            process = subprocess.Popen(command, cwd=repo_root, env=env, stdout=stdout, stderr=stderr, start_new_session=os.name == "posix")
            try:
                result["returncode"] = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                result["timed_out"] = True
                if os.name == "posix":
                    os.killpg(process.pid, signal.SIGKILL)
                else:
                    process.kill()
                process.wait()
                result["returncode"] = 124
                stderr.write("\nExperiment command exceeded its bounded timeout.\n")
        except OSError as exc:
            result["returncode"] = 127
            stderr.write(str(exc) + "\n")
    result["duration_seconds"] = round(time.monotonic() - started, 3)
    return result


def source_identity(repo_root):
    try:
        commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo_root, text=True, timeout=10).strip()
        dirty = bool(subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=normal"], cwd=repo_root, text=True, timeout=10).strip())
        return {"commit": commit, "worktree_dirty": dirty}
    except (OSError, subprocess.SubprocessError):
        return {"commit": None, "worktree_dirty": None}


def markdown_cell(value):
    return str(value).replace("|", "\\|").replace("\n", " ")


def format_rate(value):
    return "未测量" if value is None else f"{value:.1%}"


def write_summary(output, manifest):
    lines = ["# 系统有效性验证证据", "", "本次结论：**" + manifest["verdict"] + "**。仅适用于本次固定合成实验。", "",
             "源码提交：`" + str(manifest["source"]["commit"]) + "`；工作区有未提交改动：`" + str(manifest["source"]["worktree_dirty"]) + "`。", "",
             "| 验证层 | 结果 | 证据 |", "| --- | --- | --- |"]
    for command in manifest["commands"]:
        lines.append(f"| {command['name']} | 退出码 {command['returncode']}，{command['duration_seconds']:.3f} 秒 | [stdout]({command['stdout']}) / [stderr]({command['stderr']}) |")
    monitoring = manifest.get("monitoring")
    if monitoring:
        lines += ["", "## 监测链路：输入与实际输出", "", "耗时是各合成阶段的验收耗时，不是生产故障发现时间。", "",
                  "| 阶段 | 预设事实 | 应出现的告警 | 实际告警 | 耗时（毫秒） |", "| --- | --- | --- | --- | ---: |"]
        for item in monitoring["stages"]:
            lines.append("| " + " | ".join(markdown_cell(v) for v in (item["scenario"], item["ground_truth"], ", ".join(item["expected_findings"]) or "无", ", ".join(item["observed_findings"]) or "无", f"{item['elapsed_ms']:.2f}")) + " |")
        lines += ["", "## 能否区分故障和正常状态？", "", "仅纳入预先标注为正常/故障的阶段；采集失败、过期和重启回放不进入混淆矩阵。阶段不是独立样本。", "",
                  "| 方法 | 真阳性 | 误报 | 真阴性 | 漏报 | 精确率 | 召回率 | 正常阶段误报率 |", "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"]
        labels = {"observed": "本系统实测", "always_alert": "总是告警", "always_quiet": "总是不告警"}
        for name, row in monitoring["baselines"].items():
            values = [labels[name]] + [row[k] for k in ("true_positive", "false_positive", "true_negative", "false_negative")] + [format_rate(row[k]) for k in ("precision", "recall", "false_positive_rate")]
            lines.append("| " + " | ".join(map(str, values)) + " |")
    workflow = manifest.get("workflow")
    lines += ["", "## 工作流结果", ""]
    if workflow:
        lines += [f"共 {workflow['cases_run']} 个合成用例，每例 {manifest['trials']} 次；通过 {workflow['cases_passed']}/{workflow['cases_run']}，判定 {workflow['verdict']}。",
                  f"综合评分：{workflow['overall_score']:.4f}（0–1）；总分不能代替逐例通过和安全门禁。", "",
                  "[完整评分及门禁](workflow/report.json) · [工作流摘要](workflow/summary.md)"]
        if workflow["failed_cases"]:
            lines += ["", "| 失败用例 | 原因 |", "| --- | --- |"]
            for case in workflow["failed_cases"]:
                lines.append("| " + markdown_cell(case["id"]) + " | " + markdown_cell("; ".join(case["failures"]) or "见完整逐次结果") + " |")
    elif manifest["monitoring_only"]:
        lines.append("本次选择仅监测验收；工作流能力未测量，不能视为通过完整验证。")
    else:
        lines.append("工作流报告未通过验证；查看失败原因和原始日志。")
    if manifest["errors"]:
        lines += ["", "## 失败原因", ""] + ["- " + markdown_cell(error) for error in manifest["errors"]]
    lines += ["", "## 尚未证明的部分", ""] + ["- " + limit for limit in LIMITS]
    lines += ["", "原始日志只保存在本地。文件摘要与执行参数见 [manifest.json](manifest.json)；已有基线不会被覆盖。", ""]
    (output / "summary.md").write_text("\n".join(lines), encoding="utf-8")


def artifact_hashes(output):
    result = {}
    for path in sorted(output.rglob("*")):
        if path.is_file() and not path.is_symlink() and path.name != "manifest.json":
            digest = hashlib.sha256()
            with path.open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(chunk)
            result[path.relative_to(output).as_posix()] = {"sha256": digest.hexdigest(), "bytes": path.stat().st_size}
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--trials", type=int, default=1, help="descriptive workflow replays per case (1..20)")
    parser.add_argument("--output-dir", type=Path, help="new directory only; default data/eval/effectiveness/<unique-id>")
    parser.add_argument("--monitoring-only", action="store_true", help="skip the workflow benchmark; suitable for the monitoring CI gate")
    args = parser.parse_args(argv)
    if not 1 <= args.trials <= 20:
        parser.error("--trials must be between 1 and 20")
    repo_root = Path(__file__).resolve().parents[2]
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:8]
    try:
        output = reserve_output(args.output_dir or repo_root / "data/eval/effectiveness" / stamp)
    except OSError as exc:
        parser.error("cannot create new output directory: " + str(exc))
    env, removed, forced = fixture_environment(os.environ, output)
    manifest = {"schema_version": "effectiveness-evidence/v1", "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                "source": source_identity(repo_root), "trials": args.trials, "monitoring_only": args.monitoring_only,
                "removed_environment_keys": removed, "fixture_environment": forced, "commands": [], "errors": [], "limits": LIMITS}
    monitoring = run_command("monitoring", ["go", "-C", "backend", "test", "-json", "./internal/collector", "-run", "^" + ROOT_TEST + "$", "-count=1"], repo_root, output, env, 300)
    manifest["commands"].append(monitoring)
    if monitoring["returncode"]:
        manifest["errors"].append("monitoring command failed: " + str(monitoring["returncode"]))
    try:
        manifest["monitoring"] = parse_monitoring_log((output / monitoring["stdout"]).read_text(encoding="utf-8"))
    except (OSError, EvidenceError) as exc:
        manifest["errors"].append(str(exc))
    if not args.monitoring_only:
        workflow = run_command("workflow", ["go", "-C", "backend", "run", "./cmd/evalctl", "-system-perf-v2", "-scope", "benchmark", "-trials", str(args.trials), "-seed", "42", "-include-trials", "-report-dir", str(output / "workflow"), "-format", "json"], repo_root, output, env, 1200)
        manifest["commands"].append(workflow)
        if workflow["returncode"]:
            manifest["errors"].append("workflow command failed: " + str(workflow["returncode"]))
        try:
            fixture = strict_json((repo_root / "eval_data/system_perf_cases_v2.json").read_text(encoding="utf-8"))
            expected = [case["id"] for case in fixture["cases"] if "benchmark" in case["suites"]]
            report = validate_workflow_report(output / "workflow/report.json", args.trials, expected)
            manifest["workflow"] = workflow_summary(report)
        except WorkflowGateError as exc:
            manifest["workflow"] = workflow_summary(exc.report)
            manifest["errors"].append(str(exc))
        except (OSError, ValueError, KeyError, TypeError) as exc:
            manifest["errors"].append(str(exc))
    manifest["verdict"] = "FAIL" if manifest["errors"] else ("PASS_MONITORING_ONLY" if args.monitoring_only else "PASS")
    write_summary(output, manifest)
    manifest["artifacts"] = artifact_hashes(output)
    (output / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2, allow_nan=False) + "\n", encoding="utf-8")
    print(manifest["verdict"] + ": " + str(output / "summary.md"), flush=True)
    return 1 if manifest["errors"] else 0


if __name__ == "__main__":
    sys.exit(main())
