# Design

## Source of truth

Status: Active. Updated: 2026-09-12.

This contract covers the existing React console, with this iteration focused on
the platform overview, metric trends, and shared navigation. Evidence reviewed:
[App](frontend/src/App.tsx), [dashboard layout](frontend/src/components/Dashboard/Grid.tsx),
[theme tokens](frontend/src/index.css), [overview](frontend/src/components/Visualizations/MetricOverviewPanel.tsx),
[trends](frontend/src/components/Visualizations/MetricTrendsPage.tsx), their unit
tests, and [telemetry browser tests](tests/ui/e2e/telemetry-visualization.spec.ts). No previous design brief
or approved reference image was found. Existing screenshots are documentation,
not a pixel-matching target.

## Brand

An understated engineering console: precise, calm, and evidence-led. Trust comes
from timestamps, units, scope, and explicit uncertainty. Avoid decorative health
scores, fabricated live activity, and color that implies unsupported conclusions.

## Product goals

Make pressure, change, and missing telemetry easy to distinguish. Preserve
resource-to-process drilldowns and existing routes. This pass is not a new
dashboard framework, backend API redesign, or remediation workflow.

Success means readable charts in both themes, accurate temporal spacing,
accessible navigation, and no reassuring interpretation while data is absent.

## Personas and jobs

Assumed primary users are on-call SREs and Linux/GPU operators. They need to
identify a signal, establish its time and scope, and inspect supporting evidence
without mistaking collection failure for a healthy zero.

## Information architecture

Keep the existing navigation and URL page slugs. Overview provides current
resource readings and compact history; Metric Trends provides detailed curves,
anomalies, and process drilldowns. Scope and observation time precede readings;
interpretations remain subordinate to data quality.

## Design principles

- Show observations rather than inventing smooth behavior between them.
- Use elapsed time, not categorical clock labels, to position time-series data.
- Missing/non-finite values are unavailable or gaps; valid zero remains zero.
- Use words alongside color for freshness, warnings, and selected navigation.
- Preserve saved desktop layouts and established component boundaries.

## Visual language

Reuse the HSL tokens in `frontend/src/index.css` and existing Tailwind spacing,
typography, and radii. Add resource-series tokens there, not a parallel theme.
Light-theme lines need sufficient contrast against white; dark-theme lines use
lighter variants. Keep labels neutral, tabular values prominent, and area fills
subtle. Use existing Lucide icons. No generated imagery is needed.

## Components

Reuse MetricOverviewPanel, MetricCurveCard, the existing grid, and native controls.
Chart preparation/formatting may be shared where it expresses the same contract.
Show compact ranges and sample context beside mini-charts. Tooltips use existing
popover/foreground/border tokens. Detailed curves offer a native observation
disclosure with a semantic table: local observation time, unrounded numeric value,
and a textual observed/anomaly/missing status. State the source unit and timezone;
sort rows by elapsed time using the same prepared points as the curve. Preserve
zero and gaps, and omit invalid timestamps. Mark observations around missing-value
gaps so isolated readings remain visible. No additional dependencies.

## Accessibility

Target WCAG 2.2 AA; this is a target, not certification. Name navigation and
controls, expose the current page, retain visible keyboard focus, and provide
text summaries for charts. Do not make hover essential. Announce loading/errors
without announcing every polling update. Honor reduced motion.
Observation disclosures work with keyboard and touch. Give their bounded scrolling
regions accessible names and keyboard focus; mark table headers explicitly.

## Responsive behavior

Support desktop and narrow screens down to 360px. Shared chrome wraps rather
than pushing content outside the viewport. Navigation scrolls independently when
its items exceed the available height. Dashboard cards reflow to one column on
narrow screens; desktop drag/resize preferences remain unchanged.
Detailed chart headers wrap and time-axis tick density adapts to the card width.
Observation tables wrap within the card instead of widening the page. Keep process
drilldown and disclosure targets at least 44px high.

## Interaction states

Distinguish loading, unavailable, empty, fresh, stale, delayed, and degraded data.
Do not say "no anomalies" or "data available" during failed/pending requests.
Show an explicit retry for overview fetch errors. Retained stale observations
must not be presented as current verified health.

## Content voice

Use concise operator language, explicit units, and precise timestamps. Prefer
"No observations" to a fake zero and "No detected anomalies in available data"
to an unqualified healthy-state claim.

## Implementation constraints

React, TypeScript, Tailwind, Recharts, and React Query remain the supported stack.
Keep API contracts unchanged. Regression tests cover numeric validity and query
states; real-browser tests cover actual SVG rendering, themes, tooltips, and
narrow-layout overflow, including keyboard and touch observation inspection.
Mount observation rows only while expanded; use the existing bounded timeseries
query rather than introducing another data source. Unit chart mocks are not visual proof. Screenshots and
fixtures must use synthetic data; never publish real host telemetry or local
identifiers. Generated build output is not part of source commits.

## Open questions

- [ ] Product owner: confirm whether touch-first dashboard rearrangement is a
  priority. This pass preserves desktop editing and prioritizes narrow-screen
  readability.
- [ ] Maintainers: expand contrast and assistive-technology auditing to the
  remaining investigation and GPU surfaces; this pass does not certify them.
