import { expect, Page, test } from '@playwright/test';

const NAV_TARGETS: Array<{ title: string; heading: RegExp }> = [
  { title: 'Dashboard', heading: /SRE Command Center/i },
  { title: 'Metric Trends', heading: /Metric Trends/i },
  { title: 'Incident Analysis', heading: /Incident \/ Analysis/i },
  { title: 'Risk Insights', heading: /Risk Insights/i },
  { title: 'Joint Risk', heading: /Joint Risk/i },
  { title: 'RCA Workflow', heading: /RCA Workflow/i },
  { title: 'Security Dashboard', heading: /Security Dashboard/i },
  { title: 'Incidents', heading: /Incidents/i },
  { title: 'Audit Log', heading: /Action Audit Log/i },
  { title: 'Logs', heading: /Logs/i },
  { title: 'GPU Observability', heading: /GPU Observability/i },
  { title: 'AGENT', heading: /AGENT Operations/i },
  { title: 'Data Path Diagnostics', heading: /Data Path Diagnostics/i },
];

async function openSection(page: Page, title: string, heading: RegExp): Promise<void> {
  await page.locator(`button[title="${title}"]`).click();
  await page.waitForLoadState('networkidle');
  await expect(page.getByRole('heading', { level: 1, name: heading })).toBeVisible();
}

test.describe('UI Navigation Smoke', () => {
  test('navigates all major sections without runtime errors', async ({ page }) => {
    const consoleErrors: string[] = [];
    const runtimeErrors: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });
    page.on('pageerror', (error) => runtimeErrors.push(String(error)));

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    for (const target of NAV_TARGETS) {
      await openSection(page, target.title, target.heading);
    }

    expect(runtimeErrors).toEqual([]);
    expect(consoleErrors).toEqual([]);
  });

  test('diagnostics, logs, and k8s endpoints respond with supported statuses', async ({ request }) => {
    const expectations: Array<{ path: string; allow: number[] }> = [
      { path: '/api/v1/diagnostics/data-path', allow: [200] },
      { path: '/api/v1/diagnostics/kernel-path', allow: [200] },
      { path: '/api/v1/diagnostics/root-cause', allow: [200, 503] },
      { path: '/api/v1/diagnostics/workload-path', allow: [200, 503] },
      { path: '/api/v1/analysis/status', allow: [200] },
      { path: '/api/v1/analysis/incidents?limit=10', allow: [200] },
      { path: '/api/v1/analysis/correlations', allow: [200] },
      { path: '/api/v1/ha/status', allow: [200] },
      { path: '/api/v1/storage/status', allow: [200] },
      { path: '/api/v1/storage/retention', allow: [200] },
      { path: '/api/v1/finops/signals', allow: [200] },
      { path: '/api/v1/logs/status', allow: [200, 503] },
      { path: '/api/v1/logs/search?limit=10', allow: [200, 400, 503] },
      { path: '/api/v1/k8s/status', allow: [200] },
      { path: '/api/v1/k8s/workloads/top?metric=pressure&limit=5', allow: [200] },
      { path: '/api/v1/k8s/nodes/top?metric=pressure&limit=5', allow: [200] },
      { path: '/api/v1/agent/status', allow: [200, 503] },
      { path: '/api/v1/agent/incidents?limit=5', allow: [200, 503] },
      { path: '/api/v1/agent/potential-risks?limit=3', allow: [200, 503] },
      { path: '/api/v1/agent/joint-risk?limit=3', allow: [200, 503] },
      { path: '/api/v1/agent/rca?limit=3', allow: [200, 503] },
      { path: '/api/v1/agent/workflow/audit?limit=10', allow: [200, 503] },
      { path: '/api/v1/agent/workflow/incidents?limit=10', allow: [200, 503] },
      { path: '/api/v1/security/dashboard?window=45m&limit=10', allow: [200] },
      { path: '/api/v1/security/findings?window=45m&limit=10', allow: [200] },
      { path: '/api/v1/security/trends?window=45m&limit=10', allow: [200] },
      { path: '/api/v1/controller/telemetry/metrics?window=45m&limit=10', allow: [200, 404] },
      { path: '/api/v1/controller/telemetry/logs?window=45m&limit=10', allow: [200, 404] },
      { path: '/api/v1/controller/telemetry/security?window=45m&limit=10', allow: [200] },
      { path: '/api/v1/controller/agent/runs?limit=10', allow: [200] },
      { path: '/api/v1/controller/audit?limit=10', allow: [200] },
      { path: '/api/v1/controller/tools', allow: [200, 503] },
    ];

    for (const item of expectations) {
      const resp = await request.get(item.path);
      expect(item.allow).toContain(resp.status());
    }
  });

  test('fleet and status payloads are structured', async ({ request }) => {
    const statusResp = await request.get('/api/v1/status');
    expect(statusResp.status()).toBe(200);
    const statusJSON = await statusResp.json();
    expect(statusJSON).toMatchObject({
      version: expect.any(String),
      uptime: expect.any(String),
    });

    const fleetResp = await request.get('/api/v1/fleet');
    expect(fleetResp.status()).toBe(200);
    const fleetJSON = await fleetResp.json();
    expect(Array.isArray(fleetJSON.nodes)).toBeTruthy();
    expect(typeof fleetJSON.count).toBe('number');
    expect(typeof fleetJSON.timestamp).toBe('string');
  });
});

test.describe('Responsive Smoke', () => {
  const viewports = [
    { width: 1920, height: 1080 },
    { width: 768, height: 1024 },
    { width: 390, height: 844 },
  ];

  for (const viewport of viewports) {
    test(`renders at ${viewport.width}x${viewport.height}`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await page.goto('/');
      await page.waitForLoadState('networkidle');
      await expect(page.getByRole('heading', { name: /SRE Command Center/i })).toBeVisible();
    });
  }
});
