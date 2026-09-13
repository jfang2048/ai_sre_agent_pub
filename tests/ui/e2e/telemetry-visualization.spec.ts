import { expect, test, type Page } from '@playwright/test';

test.use({ timezoneId: 'UTC', video: 'off' });

// Synthetic, deterministic observations only: never capture a real host here.
type FixturePoint = { timestamp: string; value: number | null; is_anomaly?: boolean };
const points: FixturePoint[] = [
  { timestamp: '2026-09-01T12:00:00Z', value: 20 },
  { timestamp: '2026-09-01T12:01:00Z', value: 60 },
  { timestamp: '2026-09-01T12:10:00Z', value: 40 },
];
const series = [
  { key: 'cpu_usage_percent', display: 'CPU Usage', unit: 'percent', latest: 40,
    min: 20, max: 60, avg: 40, change_pct: 100, spike_count: 0, trend: 'rising', points },
  { key: 'memory_used_percent', display: 'Memory Usage', unit: 'percent', latest: 58,
    min: 38, max: 78, avg: 58, change_pct: 10, spike_count: 0,
    points: points.map(point => ({ ...point, value: point.value! + 18 })) },
];

async function fixture(page: Page, theme: 'light' | 'dark', sampleMode: 'regular' | 'single' | 'empty' | 'gapped' = 'regular') {
  await page.addInitScript((theme) => {
    localStorage.setItem('dashboard-store', JSON.stringify({ version: 4, state: { theme } }));
  }, theme);
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    let body: unknown = {};
    if (path.endsWith('/timeseries')) {
      const observations: FixturePoint[] = sampleMode === 'single' ? points.slice(0, 1)
        : sampleMode === 'empty' ? [] : sampleMode === 'gapped' ? [
          points[2],
          { timestamp: '2026-09-01T12:03:00Z', value: null, is_anomaly: true },
          { ...points[1], value: 60.125, is_anomaly: true },
          { timestamp: 'invalid-time', value: 99 },
          { ...points[0], value: 0 },
        ] : points;
      const orderedObservations = observations.filter(point => Number.isFinite(Date.parse(point.timestamp)))
        .sort((left, right) => Date.parse(left.timestamp) - Date.parse(right.timestamp));
      const visibleSeries = series.map((item, index) => {
        const seriesPoints = observations.map(point => ({ ...point,
          value: point.value === null ? null : point.value + (index === 1 ? 18 : 0),
        }));
        const valid = orderedObservations.filter(point => point.value !== null)
          .map(point => point.value! + (index === 1 ? 18 : 0));
        return { ...item, points: seriesPoints, latest: valid.at(-1) ?? null,
          min: valid.length ? Math.min(...valid) : null, max: valid.length ? Math.max(...valid) : null,
          avg: valid.length ? valid.reduce((sum, value) => sum + value, 0) / valid.length : null,
          change_pct: valid.length > 1 && valid[0] !== 0 ? (valid.at(-1)! - valid[0]) / valid[0] * 100 : 0,
          spike_count: orderedObservations.filter(point => point.value !== null && point.is_anomaly).length,
          trend: valid.length > 1 ? item.trend : 'stable' };
      });
      body = { collector_id: '', hostname: '', window: '30m', generated_at: points[2].timestamp,
        latest_at: orderedObservations.at(-1)?.timestamp, sample_count: orderedObservations.length, series: visibleSeries,
        numeric_summary: sampleMode === 'empty' ? {} : { cpu_usage_percent: visibleSeries[0].latest, memory_used_percent: visibleSeries[1].latest,
          memory_used_bytes: 8 * 1024 ** 3, memory_total_bytes: 16 * 1024 ** 3,
          network_rx_bytes_per_second: 2048, network_tx_bytes_per_second: 1024,
          disk_read_bytes_per_second: 4096, disk_write_bytes_per_second: 2048, procs_running: 12 },
        telemetry_quality: { state: 'fresh', coverage_percent: 100 }, operational_insights: [] };
    } else if (path === '/api/v1/status') {
      body = { version: 'v0.95', uptime: '30m', total_nodes: 2, healthy_nodes: 2,
        collector_coverage: { state: 'fresh', total_collectors: 2, fresh_collectors: 2,
          coverage_percent: 100 } };
    } else if (path === '/api/v1/fleet') {
      body = { nodes: [], count: 0, timestamp: points[2].timestamp };
    } else {
      // Other dashboard surfaces are outside this isolated visualization test.
      await route.fulfill({ status: 503, contentType: 'application/json', body: '{}' });
      return;
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
  });
}

for (const theme of ['light', 'dark'] as const) {
  test(`overview charts and navigation render at desktop and mobile in ${theme}`, async ({ page }) => {
    const runtimeErrors: string[] = [];
    page.on('pageerror', error => runtimeErrors.push(error.message));
    await fixture(page, theme);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/');
    await expect(page.getByText('CPU Usage', { exact: true })).toBeVisible();
    await expect(page.locator('.recharts-area-curve').first()).toBeVisible();
    const desktopLayout = await page.evaluate(() => JSON.parse(localStorage.getItem('dashboard-store')!).state.layout);
    await page.screenshot({ path: `test-results/screenshots/overview-${theme}.png` });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByText('CPU Usage', { exact: true })).toBeVisible();
    await expect(page.getByTitle('Toggle Theme')).toBeInViewport();
    await expect(page.getByTitle('Open Settings')).toBeInViewport();
    expect(await page.locator('main').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await page.screenshot({ path: `test-results/screenshots/overview-${theme}-mobile.png` });
    await page.setViewportSize({ width: 360, height: 740 });
    expect(await page.locator('main').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await expect(page.getByTitle('Open Settings')).toBeInViewport();
    await page.setViewportSize({ width: 1440, height: 1000 });
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem('dashboard-store')!).state.layout)).toEqual(desktopLayout);
    expect(runtimeErrors).toEqual([]);
  });
}

test('irregular samples retain elapsed-time spacing and a readable tooltip', async ({ page }) => {
  await fixture(page, 'light');
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/?page=trends');
  const curve = page.locator('.recharts-line-curve').first();
  await expect(curve).toBeVisible();
  const path = await curve.getAttribute('d');
  expect(path).not.toBeNull();
  const coordinates = [...path!.matchAll(/[ML]([\d.\-]+),([\d.\-]+)/g)].map(match => Number(match[1]));
  expect(coordinates).toHaveLength(3);
  expect((coordinates[1] - coordinates[0]) / (coordinates[2] - coordinates[0])).toBeCloseTo(0.1, 2);
  const chart = page.locator('.recharts-wrapper').first();
  await chart.scrollIntoViewIfNeeded();
  const box = await chart.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.move(box!.x + coordinates[1], box!.y + box!.height / 2);
  await expect(page.locator('.recharts-tooltip-wrapper').first()).toBeVisible();
  await expect(page.locator('.recharts-tooltip-wrapper').first()).toContainText('12:01:00');
  await page.getByRole('img', { name: /CPU Usage over time/ }).locator('..').screenshot({ path: 'test-results/screenshots/trend-time-spacing.png' });
});

test('a single observation is visible without implying a trend', async ({ page }) => {
  await fixture(page, 'light', 'single');
  await page.goto('/');
  const cpu = page.getByRole('region', { name: 'CPU Usage', exact: true });
  await expect(cpu.getByText('Single observation · trend unavailable')).toBeVisible();
  await expect(cpu.locator('.recharts-area-dot')).toBeVisible();
  await expect(cpu.getByText('rising', { exact: true })).toHaveCount(0);
});

for (const theme of ['light', 'dark'] as const) {
  test(`overview preserves isolated points and labels missing values in ${theme}`, async ({ page }) => {
    await fixture(page, theme, 'gapped');
    await page.setViewportSize({ width: 360, height: 740 });
    await page.goto('/');
    const cpu = page.getByRole('region', { name: 'CPU Usage', exact: true });
    await expect(cpu.getByText('3 observations · 1 missing value')).toBeVisible();
    const markers = cpu.locator('.recharts-area-dot');
    await expect(markers).toHaveCount(3);
    for (const marker of await markers.all()) {
      await expect(marker).toBeVisible();
      expect(Number(await marker.getAttribute('r'))).toBeGreaterThan(0);
    }
    const curve = await cpu.locator('.recharts-area-curve').getAttribute('d');
    // Separate subpaths prove the curve does not connect across the missing value.
    expect(curve!.match(/M/g)).toHaveLength(2);
    expect(await cpu.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    await cpu.screenshot({ path: `test-results/screenshots/overview-gaps-${theme}-mobile.png` });
  });
}

test('empty metric curves are explicitly unavailable, not a healthy flat line', async ({ page }) => {
  await fixture(page, 'dark', 'empty');
  await page.goto('/?page=trends');
  await expect(page.getByText('No valid observations for this metric.').first()).toBeVisible();
  await expect(page.locator('.recharts-line-curve')).toHaveCount(0);
  await expect(page.getByText('No detected anomalies in available data.')).toHaveCount(0);
});

test('exact observations are keyboard accessible, chronological, and explicit about missing values', async ({ page }) => {
  await fixture(page, 'light', 'gapped');
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/?page=trends');
  const cpu = page.getByRole('region', { name: 'CPU Usage trend', exact: true });
  const observationMarkers = cpu.locator('.recharts-line').first().locator('.recharts-line-dot');
  await expect(observationMarkers).toHaveCount(3);
  for (const marker of await observationMarkers.all()) {
    await expect(marker).toBeVisible();
    expect(Number(await marker.getAttribute('r'))).toBeGreaterThan(0);
  }
  const summary = cpu.locator('summary');
  const table = cpu.getByRole('table', { name: 'CPU Usage observations', includeHidden: true });
  await expect(table).toHaveCount(0);
  await expect(summary).toContainText('Inspect observations');
  await summary.focus();
  await expect(summary).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(table).toBeVisible();
  await expect(table.getByRole('columnheader')).toHaveText(['Observed (local time)', 'Value', 'Status']);
  await expect(cpu.getByText(/Source unit: percent/)).toBeVisible();
  const rows = table.locator('tbody tr');
  await expect(rows).toHaveCount(4);
  const expected = [
    { time: '12:00:00', value: '0', status: 'Observed' },
    { time: '12:01:00', value: '60.125', status: 'Anomaly' },
    { time: '12:03:00', value: 'Unavailable', status: 'Missing value' },
    { time: '12:10:00', value: '40', status: 'Observed' },
  ];
  for (const [index, observation] of expected.entries()) {
    const row = rows.nth(index);
    await expect(row.locator('time')).toContainText(observation.time);
    await expect(row.locator('time')).toHaveAttribute('datetime', `2026-09-01T${observation.time}.000Z`);
    await expect(row.getByRole('cell').nth(1)).toHaveText(observation.value);
    await expect(row.getByRole('cell').nth(2)).toHaveText(observation.status);
  }
  await expect(table).not.toContainText('invalid-time');
  await expect(summary).toBeFocused();
  await page.keyboard.press('Space');
  await expect(table).toHaveCount(0);
});

test.describe('touch observation disclosure', () => {
  test.use({ hasTouch: true });

  for (const theme of ['light', 'dark'] as const) {
    test(`exact observations fit a 360px screen in ${theme}`, async ({ page }) => {
      await fixture(page, theme, 'gapped');
      await page.setViewportSize({ width: 360, height: 740 });
      await page.goto('/?page=trends');
      const cpu = page.getByRole('region', { name: 'CPU Usage trend', exact: true });
      const summary = cpu.locator('summary');
      const table = cpu.getByRole('table', { name: 'CPU Usage observations', includeHidden: true });
      await summary.tap();
      await expect(table).toBeVisible();
      await expect(table.getByRole('cell', { name: '60.125', exact: true })).toBeVisible();
      await expect(table.getByRole('cell', { name: 'Missing value', exact: true })).toBeVisible();
      for (const surface of [page.locator('main'), cpu, table]) {
        expect(await surface.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
      }
      const cardBounds = await cpu.boundingBox();
      expect(cardBounds).not.toBeNull();
      for (const cell of await table.getByRole('cell').all()) {
        const bounds = await cell.boundingBox();
        expect(bounds).not.toBeNull();
        expect(bounds!.x).toBeGreaterThanOrEqual(cardBounds!.x);
        expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(cardBounds!.x + cardBounds!.width);
      }
      // Retain the mobile width while giving the full card room below the app header.
      await page.setViewportSize({ width: 360, height: 1100 });
      await cpu.scrollIntoViewIfNeeded();
      const screenshotBounds = await cpu.boundingBox();
      const mainBounds = await page.locator('main').boundingBox();
      expect(screenshotBounds!.y).toBeGreaterThanOrEqual(mainBounds!.y);
      expect(screenshotBounds!.y + screenshotBounds!.height).toBeLessThanOrEqual(mainBounds!.y + mainBounds!.height);
      expect(screenshotBounds!.x).toBeGreaterThanOrEqual(mainBounds!.x);
      expect(screenshotBounds!.x + screenshotBounds!.width).toBeLessThanOrEqual(mainBounds!.x + mainBounds!.width);
      await cpu.screenshot({ path: `test-results/screenshots/trend-observations-${theme}-mobile.png` });
      await page.setViewportSize({ width: 360, height: 740 });
      await summary.tap();
      await expect(table).toHaveCount(0);
    });
  }
});
