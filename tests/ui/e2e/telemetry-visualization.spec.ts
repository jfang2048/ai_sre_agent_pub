import { expect, test, type Page } from '@playwright/test';

test.use({ timezoneId: 'UTC', video: 'off' });

// Synthetic, deterministic observations only: never capture a real host here.
const points = [
  { timestamp: '2026-09-01T12:00:00Z', value: 20 },
  { timestamp: '2026-09-01T12:01:00Z', value: 60 },
  { timestamp: '2026-09-01T12:10:00Z', value: 40 },
];
const series = [
  { key: 'cpu_usage_percent', display: 'CPU Usage', unit: 'percent', latest: 40,
    min: 20, max: 60, avg: 40, change_pct: 100, spike_count: 0, trend: 'rising', points },
  { key: 'memory_used_percent', display: 'Memory Usage', unit: 'percent', latest: 58,
    min: 38, max: 78, avg: 58, change_pct: 10, spike_count: 0,
    points: points.map(point => ({ ...point, value: point.value + 18 })) },
];

async function fixture(page: Page, theme: 'light' | 'dark') {
  await page.addInitScript((theme) => {
    localStorage.setItem('dashboard-store', JSON.stringify({ version: 4, state: { theme } }));
  }, theme);
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    let body: unknown = {};
    if (path.endsWith('/timeseries')) {
      body = { collector_id: '', hostname: '', window: '30m', generated_at: points[2].timestamp,
        latest_at: points[2].timestamp, sample_count: points.length, series,
        numeric_summary: { cpu_usage_percent: 40, memory_used_percent: 58,
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
