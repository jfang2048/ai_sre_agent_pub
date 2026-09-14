#!/usr/bin/env python3
"""Render evaluation v2 report.json into readable static figures.

Usage: render_report.py <report.json> <figures_dir>

Static PNG figures for the benchmark report. Design rules applied:
- one y-axis per chart (never dual-axis)
- categorical colors in fixed slot order, never cycled
- thin bars with direct value labels; recessive grid
- status colors reserved for safety-related marks
"""

import json
import math
import os
import re
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

# Categorical palette (fixed slot order) and reserved status colors.
CATEGORICAL = ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300"]
STATUS = {"good": "#0ca30c", "warning": "#fab219", "critical": "#d64545"}
INK = "#26251f"
MUTED = "#6b6863"
GRID = "#e4e2dc"
SURFACE = "#ffffff"
TRACK = "#f0efeb"

plt.rcParams.update(
    {
        "figure.facecolor": SURFACE,
        "axes.facecolor": SURFACE,
        "axes.edgecolor": GRID,
        "axes.labelcolor": INK,
        "text.color": INK,
        "xtick.color": INK,
        "ytick.color": INK,
        "grid.color": GRID,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "font.size": 10,
        "axes.titlesize": 11,
        "figure.dpi": 110,
    }
)


def finite_number(value):
    """Return a finite float, or None for absent/unmeasured values."""
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    value = float(value)
    return value if math.isfinite(value) else None


def metric_value(mapping, key):
    if not isinstance(mapping, dict) or key not in mapping:
        return None
    return finite_number(mapping[key])


def metric_supported(mapping, key):
    """Return True only when the report records positive support for a metric."""
    if not isinstance(mapping, dict):
        return False
    support = mapping.get("metric_support")
    if not isinstance(support, dict):
        return False
    count = finite_number(support.get(key))
    return count is not None and count > 0


def case_token_count(case):
    """Return the per-trial token mean persisted in a case aggregate."""
    aggregate = case.get("aggregate", {}) if isinstance(case, dict) else {}
    value = metric_value(aggregate, "total_tokens")
    if value is not None:
        return value
    # Compatibility with early hand-written report fixtures.
    return metric_value(aggregate, "mean_tokens")


def direct_labels(ax, values, positions=None, fmt="{:.0%}", offset=0.01, horizontal=False):
    """Attach value labels directly to bar ends."""
    if positions is None:
        positions = range(len(values))
    for x, value in zip(positions, values):
        if value is None:
            continue
        if horizontal:
            ax.text(value + offset, x, fmt.format(value), va="center", fontsize=9, color=INK)
        else:
            ax.text(x, value + offset, fmt.format(value), ha="center", fontsize=9, color=INK)


def style_bars(ax, horizontal=False):
    ax.grid(axis="y", linewidth=0.5, alpha=0.8)
    if horizontal:
        ax.grid(axis="x", linewidth=0.5, alpha=0.8)
        ax.grid(axis="y", visible=False)
    ax.tick_params(length=0)
    for spine in ("top", "right", "left" if not horizontal else "top", "right"):
        ax.spines[spine].set_visible(False)


def fig_path(figures_dir, name):
    os.makedirs(figures_dir, exist_ok=True)
    return os.path.join(figures_dir, name)


def no_data(ax, title, message="No measured data in this report"):
    """Render an explicit empty state instead of silently plotting zeroes."""
    ax.set_title(title)
    ax.text(
        0.5,
        0.5,
        message,
        transform=ax.transAxes,
        ha="center",
        va="center",
        color=MUTED,
        fontsize=10,
    )
    ax.set_xticks([])
    ax.set_yticks([])
    for spine in ax.spines.values():
        spine.set_visible(False)


def scorecard_rows(report):
    scorecard = report.get("scorecard", {})
    dims = [
        ("task_outcome", "Task Outcome"),
        ("diagnosis_quality", "Diagnosis Quality"),
        ("safety_governance", "Safety & Governance"),
        ("trajectory_tool", "Trajectory & Tool"),
        ("efficiency", "Efficiency"),
        ("reliability", "Reliability"),
        ("collaboration_artifacts", "Collaboration"),
    ]
    explicit_measured = scorecard.get("measured")
    measured = set(explicit_measured) if isinstance(explicit_measured, list) else None
    rows = []
    for key, label in dims:
        value = metric_value(scorecard, key)
        is_measured = value is not None and (measured is None or key in measured)
        rows.append((key, label, value if is_measured else None))
    return rows


def render_scorecard(report, figures_dir):
    scorecard = report.get("scorecard", {})
    rows = scorecard_rows(report)
    names = [label for _, label, _ in rows]
    values = [value for _, _, value in rows]
    passing = metric_value(report.get("config", {}), "passing_threshold")
    passing = 0.60 if passing is None else passing

    fig, ax = plt.subplots(figsize=(8.4, 4.1))
    y = list(range(len(names)))
    # Recessive tracks preserve the scale while unmeasured dimensions remain N/A.
    ax.barh(y, [1.0] * len(y), color=TRACK, height=0.58)
    for position, value in zip(y, values):
        if value is None:
            ax.text(0.02, position, "N/A — not measured", va="center", color=MUTED, fontsize=9)
            continue
        color = STATUS["good"] if value >= passing else STATUS["critical"]
        ax.barh(position, value, color=color, height=0.58)
        ax.text(value + 0.012, position, f"{value:.0%}", va="center", fontsize=9, color=INK)
    ax.set_yticks(list(y), labels=names)
    ax.invert_yaxis()
    ax.axvline(passing, color=MUTED, linewidth=1, linestyle="--")
    ax.text(
        passing + 0.008,
        0.98,
        f"pass threshold {passing:.0%}",
        transform=ax.get_xaxis_transform(),
        color=MUTED,
        fontsize=8,
        va="top",
    )
    ax.set_xlim(0, 1.12)
    overall = metric_value(scorecard, "overall_score")
    overall_text = "N/A" if overall is None else f"{overall:.1%}"
    verdict = str(report.get("verdict") or "n/a").upper()
    gates = report.get("gates", {})
    gate_text = "PASS" if gates.get("passed") is True else "FAIL" if gates.get("passed") is False else "N/A"
    ax.set_title(f"Scorecard — overall {overall_text} · verdict {verdict} · hard gates {gate_text}")
    style_bars(ax, horizontal=True)
    fig.tight_layout()
    fig.savefig(fig_path(figures_dir, "scorecard.png"))
    plt.close(fig)


def render_incident_type_success(report, figures_dir):
    cases = report.get("cases", [])
    groups = {}
    for case in cases:
        value = metric_value(case, "task_success_rate")
        if value is not None:
            groups.setdefault(case.get("incident_type") or "unspecified", []).append(value)
    names = sorted(groups)
    values = [sum(groups[name]) / len(groups[name]) for name in names]
    labels = [f"{name}\n(n={len(groups[name])})" for name in names]

    fig, ax = plt.subplots(figsize=(7.2, 3.4))
    if not names:
        no_data(ax, "Task success by incident type")
        fig.tight_layout()
        fig.savefig(fig_path(figures_dir, "incident_type_success.png"))
        plt.close(fig)
        return
    x = range(len(names))
    ax.bar(list(x), values, color=CATEGORICAL[0], width=0.58)
    ax.set_xticks(list(x), labels=labels, rotation=20, ha="right")
    ax.set_ylim(0, 1.12)
    direct_labels(ax, values)
    ax.set_ylabel("Task success rate")
    ax.set_title("Task success by incident type")
    style_bars(ax)
    fig.tight_layout()
    fig.savefig(fig_path(figures_dir, "incident_type_success.png"))
    plt.close(fig)


def render_rca(report, figures_dir):
    agg = report.get("aggregate", {})
    metrics = []
    if metric_supported(agg, "diagnosis_quality"):
        metrics = [
            ("Entity Precision", metric_value(agg, "root_cause_entity_precision")),
            ("Entity Recall", metric_value(agg, "root_cause_entity_recall")),
            ("Entity F1", metric_value(agg, "root_cause_entity_f1")),
            ("Recall@1", metric_value(agg, "root_cause_entity_recall_at_1")),
            ("Reasoning", metric_value(agg, "root_cause_reasoning_score")),
            ("Fault Localization", metric_value(agg, "fault_localization_score")),
        ]
    metrics = [(name, value) for name, value in metrics if value is not None]
    names = [name for name, _ in metrics]
    values = [value for _, value in metrics]

    fig, ax = plt.subplots(figsize=(7.2, 3.4))
    if not names:
        no_data(ax, "Root cause analysis metrics")
        fig.tight_layout()
        fig.savefig(fig_path(figures_dir, "rca.png"))
        plt.close(fig)
        return
    x = range(len(names))
    ax.bar(list(x), values, color=CATEGORICAL[0], width=0.58)
    ax.set_xticks(list(x), labels=names, rotation=20, ha="right")
    ax.set_ylim(0, 1.12)
    direct_labels(ax, values)
    ax.set_ylabel("Score")
    ax.set_title("Root cause analysis metrics")
    style_bars(ax)
    fig.tight_layout()
    fig.savefig(fig_path(figures_dir, "rca.png"))
    plt.close(fig)


def comparison_unit_group(row):
    units = str(row.get("units") or "").lower()
    metric = str(row.get("metric") or "")
    if units.startswith("rate"):
        return "rate"
    if units == "ms" or metric.endswith("_ms"):
        return "ms"
    if units == "tokens" or "token" in metric:
        return "tokens"
    if units == "calls" or "call" in metric:
        return "calls"
    return units or "value"


def baseline_groups(report):
    """Group valid comparison rows by a single compatible display unit."""
    comparison = report.get("comparison") or {}
    groups = {}
    for row in comparison.get("rows") or []:
        baseline = metric_value(row, "baseline")
        current = metric_value(row, "current")
        if baseline is None or current is None:
            continue
        groups.setdefault(comparison_unit_group(row), []).append((row, baseline, current))
    order = {"rate": 0, "ms": 1, "tokens": 2, "calls": 3, "value": 4}
    return sorted(groups.items(), key=lambda item: (order.get(item[0], 99), item[0]))


def fired_metric_severity(comparison):
    severity = {}
    for fired in comparison.get("fired_rules") or []:
        match = re.match(r"\[(fail|warn)\]\s+([^:]+):", str(fired), re.IGNORECASE)
        if match:
            severity[match.group(2)] = match.group(1).lower()
    return severity


def display_value(value, unit):
    return value * 100 if unit == "rate" else value


def display_unit(unit):
    return {
        "rate": "Percent",
        "ms": "Milliseconds",
        "tokens": "Tokens",
        "calls": "Tool calls",
        "value": "Value",
    }.get(unit, unit)


def format_display_value(value, unit):
    if unit == "rate":
        return f"{value:.1f}%"
    if unit == "ms":
        return f"{value:,.0f} ms"
    if unit == "tokens":
        return f"{value:,.0f}"
    if unit == "calls":
        return f"{value:.1f}"
    return f"{value:.3g}"


def format_delta(delta, unit):
    if unit == "rate":
        return f"Δ {delta:+.1f} pp"
    suffix = {"ms": " ms", "tokens": " tokens", "calls": " calls"}.get(unit, "")
    decimals = 1 if unit == "calls" else 0
    return f"Δ {delta:+,.{decimals}f}{suffix}"


def render_baseline(report, figures_dir):
    comparison = report.get("comparison")
    if not comparison or not comparison.get("rows"):
        return
    groups = baseline_groups(report)
    if not groups:
        return
    row_count = sum(len(rows) for _, rows in groups)
    fig, axes = plt.subplots(
        len(groups),
        1,
        figsize=(9.2, max(3.2, 0.72 * row_count + 1.8 * len(groups))),
        squeeze=False,
    )
    severity = fired_metric_severity(comparison)
    for index, (unit, rows) in enumerate(groups):
        ax = axes[index][0]
        names = []
        baseline = []
        current = []
        current_colors = []
        for row, baseline_value, current_value in rows:
            baseline_display = display_value(baseline_value, unit)
            current_display = display_value(current_value, unit)
            delta = current_display - baseline_display
            metric = str(row.get("metric") or "unnamed")
            names.append(f"{metric.replace('_', ' ')}\n{format_delta(delta, unit)}")
            baseline.append(baseline_display)
            current.append(current_display)
            level = severity.get(metric)
            current_colors.append(
                STATUS["critical"]
                if level == "fail"
                else STATUS["warning"]
                if level == "warn"
                else CATEGORICAL[0]
            )
        y = list(range(len(names)))
        height = 0.34
        ax.barh([i - height / 2 for i in y], baseline, height=height, color=MUTED, label="baseline")
        ax.barh([i + height / 2 for i in y], current, height=height, color=current_colors, label="current")
        ax.set_yticks(y, labels=names, fontsize=8)
        ax.invert_yaxis()
        max_value = max(baseline + current) if baseline or current else 1
        padding = max(max_value * 0.015, 0.15 if unit == "rate" else 0.01)
        for position, value in zip([i - height / 2 for i in y], baseline):
            ax.text(value + padding, position, format_display_value(value, unit), va="center", fontsize=8, color=MUTED)
        for position, value in zip([i + height / 2 for i in y], current):
            ax.text(value + padding, position, format_display_value(value, unit), va="center", fontsize=8, color=INK)
        ax.set_xlim(0, max(max_value * 1.24, 1.0))
        ax.set_xlabel(display_unit(unit))
        ax.set_title(display_unit(unit), loc="left", color=MUTED, fontsize=9)
        style_bars(ax, horizontal=True)
    verdict = str(comparison.get("verdict") or "n/a").upper()
    fig.suptitle(f"Baseline vs current — regression verdict {verdict}", y=0.995, fontsize=12)
    legend_handles = [
        plt.Line2D([], [], linewidth=7, color=MUTED, label="baseline"),
        plt.Line2D([], [], linewidth=7, color=CATEGORICAL[0], label="current"),
    ]
    if "warn" in severity.values():
        legend_handles.append(plt.Line2D([], [], linewidth=7, color=STATUS["warning"], label="warning"))
    if "fail" in severity.values():
        legend_handles.append(plt.Line2D([], [], linewidth=7, color=STATUS["critical"], label="failed gate"))
    fig.legend(
        handles=legend_handles,
        frameon=False,
        ncols=len(legend_handles),
        loc="upper center",
        bbox_to_anchor=(0.5, 0.981),
    )
    fig.tight_layout(rect=(0, 0, 1, 0.955))
    fig.savefig(fig_path(figures_dir, "baseline_comparison.png"))
    plt.close(fig)


def render_cost_vs_success(report, figures_dir):
    cases = report.get("cases", [])
    points = []
    for case in cases:
        tokens = case_token_count(case)
        success = metric_value(case, "task_success_rate")
        if tokens is None or success is None:
            continue
        points.append((case, tokens, success))
    task_types = sorted({case.get("task_type") or "unspecified" for case, _, _ in points})
    color_by_type = {
        task: CATEGORICAL[i] if i < len(CATEGORICAL) else MUTED for i, task in enumerate(task_types)
    }
    xs = [tokens for _, tokens, _ in points]
    ys = [success for _, _, success in points]
    colors = [color_by_type[case.get("task_type") or "unspecified"] for case, _, _ in points]

    fig, ax = plt.subplots(figsize=(7.2, 3.8))
    if not points:
        no_data(ax, "Cost vs success", "No cases have both token and success measurements")
        fig.tight_layout()
        fig.savefig(fig_path(figures_dir, "cost_vs_success.png"))
        plt.close(fig)
        return
    ax.scatter(xs, ys, c=colors, s=34, alpha=0.85, edgecolors=SURFACE, linewidths=0.5)
    ax.set_xlabel("Mean tokens per trial")
    ax.set_ylabel("Task success rate")
    ax.set_ylim(-0.06, 1.08)
    passing = metric_value(report.get("config", {}), "passing_threshold")
    passing = 0.60 if passing is None else passing
    median_tokens = sorted(xs)[len(xs) // 2]
    ax.axhline(passing, color=MUTED, linewidth=0.8, linestyle="--")
    ax.axvline(median_tokens, color=GRID, linewidth=0.8, linestyle="--")
    expensive_failures = sorted(
        [point for point in points if point[1] >= median_tokens and point[2] < passing],
        key=lambda point: point[1],
        reverse=True,
    )[:4]
    if expensive_failures:
        labels = [
            f"• {str(case.get('id') or 'unnamed')[:30]} ({tokens / 1000:.1f}k)"
            for case, tokens, _ in expensive_failures
        ]
        ax.text(
            0.98,
            0.43,
            "High-cost misses\n" + "\n".join(labels),
            transform=ax.transAxes,
            ha="right",
            va="top",
            fontsize=7,
            color=MUTED,
            bbox={"boxstyle": "round,pad=0.35", "facecolor": SURFACE, "edgecolor": GRID, "alpha": 0.92},
        )
    ax.set_title("Cost vs success — preferred direction: up and left")
    handles = [
        plt.Line2D([], [], marker="o", linestyle="", color=color_by_type[task], label=task)
        for task in task_types
    ]
    ax.legend(handles=handles, frameon=False, fontsize=8)
    style_bars(ax)
    fig.tight_layout()
    fig.savefig(fig_path(figures_dir, "cost_vs_success.png"))
    plt.close(fig)


def render_failure_modes(report, figures_dir):
    modes = report.get("failure_modes", {})
    if not modes:
        return
    names = sorted(modes, key=lambda mode: -modes[mode])
    values = [modes[name] for name in names]
    severe_modes = {
        "unsafe_action",
        "incorrect_verification",
        "premature_termination",
        "hallucinated_evidence",
    }
    colors = [STATUS["critical"] if name in severe_modes else CATEGORICAL[1] for name in names]

    fig, ax = plt.subplots(figsize=(7.2, 3.4))
    y = range(len(names))
    ax.barh(list(y), values, color=colors, height=0.58)
    ax.set_yticks(list(y), labels=names)
    ax.invert_yaxis()
    direct_labels(ax, values, positions=list(y), fmt="{:.0f}", offset=max(values) * 0.02 + 0.1, horizontal=True)
    ax.set_title("Failure modes (MAST-style taxonomy)")
    style_bars(ax, horizontal=True)
    fig.tight_layout()
    fig.savefig(fig_path(figures_dir, "failure_modes.png"))
    plt.close(fig)


def render_stability(report, figures_dir):
    cases = report.get("cases", [])
    measured = [(case, metric_value(case, "task_success_rate")) for case in cases]
    measured = [(case, value) for case, value in measured if value is not None]
    names = [str(case.get("id") or "unnamed") for case, _ in measured]
    values = [value for _, value in measured]
    ci_available = [bool(case.get("task_success_ci_available")) for case, _ in measured]
    lows = [
        metric_value(case, "task_success_ci95_low") if available else None
        for (case, _), available in zip(measured, ci_available)
    ]
    highs = [
        metric_value(case, "task_success_ci95_high") if available else None
        for (case, _), available in zip(measured, ci_available)
    ]
    lows = [value if low is None else low for low, value in zip(lows, values)]
    highs = [value if high is None else high for high, value in zip(highs, values)]
    errors = [[max(0, v - lo) for v, lo in zip(values, lows)], [max(0, hi - v) for v, hi in zip(values, highs)]]
    flaky = [case.get("flaky", False) for case, _ in measured]

    has_ci = any(ci_available)
    trials_per_case = finite_number(report.get("environment", {}).get("trials_per_case"))
    if has_ci:
        title = "Stability — per-case task success with 95% CI"
    elif trials_per_case is not None and trials_per_case < 2:
        title = "Per-case task success — single run (no stability estimate)"
    else:
        title = "Stability — per-case task success (descriptive replay)"
    fig, ax = plt.subplots(figsize=(8.0, 4.2))
    if not measured:
        no_data(ax, title)
        fig.tight_layout()
        fig.savefig(fig_path(figures_dir, "stability.png"))
        plt.close(fig)
        return
    y = range(len(names))
    colors = [STATUS["warning"] if flag else STATUS["good"] for flag in flaky]
    bar_options = {"color": colors, "height": 0.55}
    if has_ci:
        bar_options.update(
            {
                "xerr": errors,
                "capsize": 3,
                "error_kw": {"elinewidth": 1, "ecolor": MUTED},
            }
        )
    ax.barh(list(y), values, **bar_options)
    ax.set_yticks(list(y), labels=names, fontsize=8)
    ax.invert_yaxis()
    ax.set_xlim(0, 1.12)
    direct_labels(ax, values, positions=list(y), fmt="{:.0%}", offset=0.012, horizontal=True)
    ax.set_title(title)
    style_bars(ax, horizontal=True)
    fig.tight_layout()
    fig.savefig(fig_path(figures_dir, "stability.png"))
    plt.close(fig)


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(2)
    report_path, figures_dir = sys.argv[1], sys.argv[2]
    with open(report_path, encoding="utf-8") as handle:
        report = json.load(handle)

    render_scorecard(report, figures_dir)
    render_incident_type_success(report, figures_dir)
    render_rca(report, figures_dir)
    render_baseline(report, figures_dir)
    render_cost_vs_success(report, figures_dir)
    render_failure_modes(report, figures_dir)
    render_stability(report, figures_dir)
    print(f"figures written to {figures_dir}")


if __name__ == "__main__":
    main()
