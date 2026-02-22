"""Reasoning stage: deterministic synthesis with optional LLM refinement."""

import json
import logging
from typing import Any, Dict, List, Optional

from .config import RuntimeConfig
from .structured_log import emit
from .types import ReasoningOutput, StepTrace, TelemetryEnvelope


class LLMGateway:
    """Thin provider gateway for explicit API interaction."""

    def __init__(self, config: RuntimeConfig, logger: logging.Logger) -> None:
        self._enabled = config.llm_enabled
        self._provider = config.llm_provider
        self._model = config.llm_model
        self._api_key = config.llm_api_key
        self._base_url = config.llm_base_url
        self._timeout = config.llm_timeout_seconds
        self._logger = logger
        self._client: Optional[Any] = None
        if self._enabled:
            self._client = self._build_client()

    @property
    def enabled(self) -> bool:
        return self._enabled and self._client is not None

    @property
    def provider(self) -> str:
        if not self.enabled:
            return "deterministic"
        return self._provider

    @property
    def model(self) -> str:
        if not self.enabled:
            return "rules"
        return self._model

    def complete(self, prompt: str) -> str:
        if not self.enabled:
            raise RuntimeError("LLM gateway disabled")

        provider = self._provider
        if provider in {"openai", "local", "ollama"}:
            if self._client is None:
                raise RuntimeError("OpenAI-compatible client not initialized")
            return self._client.complete(prompt, max_tokens=1000, temperature=0.1)
        if provider == "anthropic":
            if self._client is None:
                raise RuntimeError("Anthropic client not initialized")
            return self._client.complete(prompt, max_tokens=1500, temperature=0.1)
        if provider in {"google", "gemini"}:
            return self._gemini_complete(prompt)
        raise RuntimeError(f"unsupported provider: {provider}")

    def _build_client(self) -> Optional[Any]:
        provider = self._provider
        try:
            if provider in {"openai", "local", "ollama"}:
                from sre_agent.llm.openai_client import OpenAIClient

                api_key = self._api_key or ("ollama" if provider in {"local", "ollama"} else "")
                if not api_key:
                    raise ValueError("missing API key for OpenAI-compatible provider")
                base_url = self._base_url
                if provider in {"local", "ollama"} and not base_url:
                    base_url = "http://127.0.0.1:11434/v1"
                return OpenAIClient(
                    api_key=api_key,
                    model=self._model,
                    base_url=base_url or None,
                    timeout=self._timeout,
                )

            if provider == "anthropic":
                from sre_agent.llm.anthropic_client import AnthropicClient

                if not self._api_key:
                    raise ValueError("missing API key for Anthropic provider")
                return AnthropicClient(
                    api_key=self._api_key,
                    model=self._model,
                    timeout=self._timeout,
                    max_tokens=2000,
                )

            if provider in {"google", "gemini"}:
                if not self._api_key:
                    raise ValueError("missing API key for Gemini provider")
                # Client is created lazily in _gemini_complete to keep dependency optional.
                return object()

            raise ValueError(f"unsupported provider: {provider}")
        except Exception as exc:  # pylint: disable=broad-except
            emit(
                self._logger,
                "llm_gateway_init_failed",
                level="warning",
                provider=provider,
                error=str(exc),
            )
            return None

    def _gemini_complete(self, prompt: str) -> str:
        try:
            import google.generativeai as genai  # type: ignore
        except Exception as exc:  # pylint: disable=broad-except
            raise RuntimeError(f"google-generativeai unavailable: {exc}") from exc

        genai.configure(api_key=self._api_key)
        model = genai.GenerativeModel(model_name=self._model)
        response = model.generate_content(
            prompt,
            generation_config={"temperature": 0.1, "max_output_tokens": 1200},
        )
        text = getattr(response, "text", "")
        if not text:
            raise RuntimeError("gemini returned empty response")
        return text


class Reasoner:
    """Produces an explicit decision from tool outputs and telemetry."""

    def __init__(self, config: RuntimeConfig, logger: logging.Logger) -> None:
        self._config = config
        self._logger = logger
        self._gateway = LLMGateway(config, logger)

    def synthesize(
        self,
        telemetry: TelemetryEnvelope,
        state: Dict[str, Any],
        traces: List[StepTrace],
    ) -> ReasoningOutput:
        baseline = self._deterministic_reasoning(telemetry, state, traces)
        if not self._gateway.enabled:
            return baseline

        prompt = self._build_prompt(telemetry, state, traces, baseline)
        try:
            raw = self._gateway.complete(prompt)
            candidate = self._parse_json(raw)
            if not candidate:
                baseline.notes = _append_note(
                    baseline.notes, "LLM output parse failed; deterministic result retained."
                )
                baseline.debug["llm_parse_failure"] = raw[:400]
                return baseline

            merged = self._merge_with_candidate(baseline, candidate)
            merged.provider = self._gateway.provider
            merged.model = self._gateway.model
            return merged
        except Exception as exc:  # pylint: disable=broad-except
            emit(
                self._logger,
                "llm_reasoning_failed",
                level="warning",
                request_id=telemetry.request_id,
                error=str(exc),
                provider=self._gateway.provider,
            )
            baseline.notes = _append_note(
                baseline.notes, f"LLM refinement failed ({exc}); deterministic result retained."
            )
            baseline.debug["llm_error"] = str(exc)
            return baseline

    def _deterministic_reasoning(
        self,
        telemetry: TelemetryEnvelope,
        state: Dict[str, Any],
        traces: List[StepTrace],
    ) -> ReasoningOutput:
        metric_triage = state.get("metric_triage", {})
        log_scan = state.get("log_pattern_scan", {})
        context = state.get("context_lookup", {})
        memory = state.get("memory_recall", {})

        high_signals = list(metric_triage.get("high_signals", []))
        signatures = list(log_scan.get("signatures", []))
        hints = list(log_scan.get("issue_hints", []))
        error_count = int(log_scan.get("error_count", 0))
        critical_count = int(log_scan.get("critical_count", 0))

        issue_type = _infer_issue_type(high_signals, hints)
        severity = _infer_severity(high_signals, error_count, critical_count)
        detected = bool(high_signals or error_count > 0 or signatures)

        confidence = min(0.95, 0.35 + 0.08 * len(high_signals) + 0.05 * len(signatures))
        if not detected:
            confidence = 0.15

        evidence: List[str] = []
        for signal in high_signals[:5]:
            evidence.append(f"metric:{signal.get('name')}={signal.get('value')}")
        for sig in signatures[:4]:
            evidence.append(f"log_signature:{sig.get('name')}:{sig.get('count')}")

        if context.get("snippet_count", 0):
            evidence.append(f"context_snippets:{context.get('snippet_count')}")
        if memory.get("count", 0):
            evidence.append(f"memory_hits:{memory.get('count')}")

        root_cause = self._build_root_cause(issue_type, high_signals, log_scan)
        remediation = _remediation_for(issue_type)
        notes = self._build_notes(context, memory, traces)

        if not detected:
            issue_type = "stable"
            severity = "info"
            root_cause = "No high-confidence incident signals detected in current telemetry slice."
            remediation = "Continue monitoring and re-evaluate if symptom persistence increases."

        return ReasoningOutput(
            issue_detected=detected,
            issue_type=issue_type,
            severity=severity,
            confidence=max(0.0, min(1.0, confidence)),
            root_cause=root_cause,
            remediation=remediation,
            notes=notes,
            evidence=evidence,
            provider="deterministic",
            model="rules-v1",
            debug={
                "trace_count": len(traces),
                "tool_status": {
                    trace.tool_name or trace.step_id: trace.status for trace in traces
                },
            },
        )

    def _build_root_cause(
        self,
        issue_type: str,
        high_signals: List[Dict[str, Any]],
        log_scan: Dict[str, Any],
    ) -> str:
        signal_text = ", ".join(
            f"{item.get('name')}={item.get('value')}" for item in high_signals[:3]
        )
        latest_error = str(log_scan.get("latest_error", "")).strip()

        if issue_type == "memory_pressure":
            base = "Memory pressure indicators are dominant in current metrics/logs."
        elif issue_type == "cpu_saturation":
            base = "CPU saturation is the dominant signal and likely bottleneck."
        elif issue_type == "disk_capacity":
            base = "Disk capacity or I/O pressure is likely causing service degradation."
        elif issue_type == "upstream_dependency":
            base = "Upstream connectivity/timeouts indicate an external dependency issue."
        elif issue_type == "application_error":
            base = "Application-side error signatures dominate the telemetry evidence."
        else:
            base = "Multiple weak signals detected; no single root cause dominates."

        if signal_text:
            base = f"{base} Key metrics: {signal_text}."
        if latest_error:
            base = f"{base} Latest error: {latest_error[:200]}."
        return base

    def _build_notes(
        self,
        context: Dict[str, Any],
        memory: Dict[str, Any],
        traces: List[StepTrace],
    ) -> str:
        notes: List[str] = []
        snippet_count = int(context.get("snippet_count", 0))
        if snippet_count > 0:
            notes.append(f"Used {snippet_count} runbook/document snippets.")
        memory_count = int(memory.get("count", 0))
        if memory_count > 0:
            notes.append(f"Matched {memory_count} prior incidents for this node.")

        failed_steps = [
            trace.tool_name or trace.step_id for trace in traces if trace.status != "success"
        ]
        if failed_steps:
            notes.append(f"Non-success steps: {', '.join(failed_steps)}.")

        return " ".join(notes).strip()

    def _build_prompt(
        self,
        telemetry: TelemetryEnvelope,
        state: Dict[str, Any],
        traces: List[StepTrace],
        baseline: ReasoningOutput,
    ) -> str:
        payload = {
            "request_id": telemetry.request_id,
            "node_name": telemetry.node_name,
            "metric_count": len(telemetry.metrics),
            "log_count": len(telemetry.logs),
            "tool_outputs": state,
            "execution_trace": [
                {
                    "step_id": item.step_id,
                    "tool": item.tool_name,
                    "status": item.status,
                    "duration_ms": item.duration_ms,
                    "error": item.error,
                }
                for item in traces
            ],
            "baseline_decision": {
                "issue_detected": baseline.issue_detected,
                "issue_type": baseline.issue_type,
                "severity": baseline.severity,
                "confidence": baseline.confidence,
                "root_cause": baseline.root_cause,
                "remediation": baseline.remediation,
                "notes": baseline.notes,
            },
        }
        serialized = json.dumps(payload, ensure_ascii=True)
        return (
            "You are an SRE incident reasoner. Refine the baseline decision strictly from provided facts. "
            "Return strict JSON with keys: issue_detected(bool), issue_type(str), severity(str), "
            "confidence(float 0..1), root_cause(str), remediation(str), notes(str), evidence(list[str]). "
            f"Input: {serialized}"
        )

    def _parse_json(self, raw: str) -> Optional[Dict[str, Any]]:
        text = raw.strip()
        if text.startswith("```"):
            text = text.strip("`")
            if text.lower().startswith("json"):
                text = text[4:].strip()
        try:
            parsed = json.loads(text)
            if isinstance(parsed, dict):
                return parsed
        except json.JSONDecodeError:
            pass

        start = text.find("{")
        end = text.rfind("}")
        if start >= 0 and end > start:
            try:
                parsed = json.loads(text[start : end + 1])
                if isinstance(parsed, dict):
                    return parsed
            except json.JSONDecodeError:
                return None
        return None

    def _merge_with_candidate(
        self,
        baseline: ReasoningOutput,
        candidate: Dict[str, Any],
    ) -> ReasoningOutput:
        severity = _normalize_severity(candidate.get("severity", baseline.severity))
        issue_type = str(candidate.get("issue_type", baseline.issue_type))[:120]
        confidence = _safe_float(candidate.get("confidence", baseline.confidence), baseline.confidence)
        confidence = max(0.0, min(1.0, confidence))

        evidence_raw = candidate.get("evidence", baseline.evidence)
        if isinstance(evidence_raw, list):
            evidence = [str(item)[:200] for item in evidence_raw[:20]]
        else:
            evidence = list(baseline.evidence)

        return ReasoningOutput(
            issue_detected=bool(candidate.get("issue_detected", baseline.issue_detected)),
            issue_type=issue_type or baseline.issue_type,
            severity=severity,
            confidence=confidence,
            root_cause=str(candidate.get("root_cause", baseline.root_cause))[:1500],
            remediation=str(candidate.get("remediation", baseline.remediation))[:1200],
            notes=str(candidate.get("notes", baseline.notes))[:1200],
            evidence=evidence,
            provider=baseline.provider,
            model=baseline.model,
            debug=dict(baseline.debug),
        )


def _safe_float(value: Any, fallback: float) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return fallback


def _append_note(current: str, note: str) -> str:
    if not current:
        return note
    return f"{current} {note}"


def _infer_issue_type(high_signals: List[Dict[str, Any]], hints: List[Dict[str, Any]]) -> str:
    hint_names = [str(item.get("name", "")) for item in hints]
    if hint_names:
        return hint_names[0]

    for signal in high_signals:
        name = str(signal.get("name", "")).lower()
        if "memory" in name or "oom" in name:
            return "memory_pressure"
        if "cpu" in name or "load" in name:
            return "cpu_saturation"
        if "disk" in name or "io" in name:
            return "disk_capacity"
        if "net" in name:
            return "network_saturation"
    if high_signals:
        return "resource_pressure"
    return "unknown"


def _infer_severity(
    high_signals: List[Dict[str, Any]],
    error_count: int,
    critical_count: int,
) -> str:
    severities = {str(item.get("severity", "")) for item in high_signals}
    if "critical" in severities or critical_count > 0:
        return "critical"
    if "warning" in severities or error_count > 0:
        return "warning"
    return "info"


def _normalize_severity(value: Any) -> str:
    normalized = str(value).strip().lower()
    if normalized not in {"info", "warning", "error", "critical"}:
        return "warning"
    return normalized


def _remediation_for(issue_type: str) -> str:
    if issue_type == "memory_pressure":
        return "Profile memory usage, identify largest consumers, and scale or restart affected workloads."
    if issue_type == "cpu_saturation":
        return "Identify top CPU processes, cap runaway jobs, and scale replicas if pressure persists."
    if issue_type == "disk_capacity":
        return "Audit disk growth, remove stale artifacts/logs, and increase storage capacity if needed."
    if issue_type == "upstream_dependency":
        return "Validate network path and upstream health, then apply timeout/backoff hardening."
    if issue_type == "application_error":
        return "Inspect deployment diff and stack traces, then roll back or patch failing release."
    if issue_type == "network_saturation":
        return "Check network hotspots, rebalance traffic, and tune connection pool/backpressure settings."
    return "Correlate additional telemetry and run targeted diagnostics before automated remediation."
