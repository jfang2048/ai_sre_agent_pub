import { expect, test } from '@playwright/test';

test.describe('Risk, RCA, and Security Pages', () => {
  test.setTimeout(60000);

  test('render risk, rca, security, incidents, audit, and logs drilldowns without runtime/network failures', async ({ page }) => {
    const consoleErrors: string[] = [];
    const runtimeErrors: string[] = [];
    const requestFailures: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });
    page.on('pageerror', (error) => runtimeErrors.push(String(error)));
    page.on('requestfailed', (request) => requestFailures.push(`${request.failure()?.errorText} ${request.url()}`));

    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /SRE Command Center/i })).toBeVisible();

    await page.getByTitle('Risk Insights').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /Risk Insights/i })).toBeVisible();
    await expect(page.getByTestId('risk-insights-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Ranked Potential Risks' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Correlation Details' })).toBeVisible();
    await expect.poll(
      async () => page.locator('[data-testid="risk-insights-page"] section:has-text("Ranked Potential Risks") button').count(),
      { timeout: 30000 },
    ).toBeGreaterThan(0);
    await expect(page.getByText('No potential risks generated yet.')).toHaveCount(0);
    await expect(page.getByTestId('risk-knowledge-evidence')).toBeVisible();
    await expect(page.getByText('Generating supporting knowledge...')).toHaveCount(0);

    await page.getByTitle('Joint Risk').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /Joint Risk/i })).toBeVisible();
    await expect(page.getByTestId('joint-risk-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Ranked Signals' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Correlation Drilldowns' })).toBeVisible();
    await expect.poll(
      async () => page.locator('[data-testid="joint-risk-page"] section:has-text("Ranked Signals") article').count(),
      { timeout: 30000 },
    ).toBeGreaterThan(0);
    await expect(page.getByText('No risk signals available.')).toHaveCount(0);
    await expect(page.getByTestId('joint-risk-knowledge-evidence')).toBeVisible();
    await expect(page.getByText('Generating joint-risk knowledge evidence...')).toHaveCount(0);

    await page.getByTitle('RCA Workflow').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /RCA Workflow/i })).toBeVisible();
    await expect(page.getByTestId('rca-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Structured RCA Report' })).toBeVisible();
    await expect(page.getByRole('heading', { name: /Plan → Act → Verify Trace/i })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Ranked Hypotheses' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Action Audit' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Proposed Actions' })).toBeVisible();
    await expect.poll(
      async () => page.locator('[data-testid="rca-page"] section:has-text("Ranked Hypotheses") article').count(),
      { timeout: 30000 },
    ).toBeGreaterThan(0);
    await expect(page.getByText('No structured report available.')).toHaveCount(0);
    await expect(page.getByText('No hypotheses produced yet.')).toHaveCount(0);
    await expect(page.getByText('No agent loop trace available.')).toHaveCount(0);
    await expect(page.getByTestId('rca-knowledge-evidence')).toBeVisible();
    await expect(page.getByText('Generating RCA knowledge evidence...')).toHaveCount(0);

    await page.getByTitle('Security Dashboard').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /Security Dashboard/i })).toBeVisible();
    await expect(page.getByTestId('security-dashboard-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Security Findings' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Security Risk Trend' })).toBeVisible();
    await expect.poll(
      async () => page.locator('[data-testid="security-dashboard-page"] section:has-text("Security Findings") button').count(),
      { timeout: 30000 },
    ).toBeGreaterThan(0);
    await expect(page.getByText('No findings for the selected window.')).toHaveCount(0);

    await page.getByTitle('Incidents').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /Incidents/i })).toBeVisible();
    await expect(page.getByTestId('incidents-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Incident List' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Incident Timeline' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Recommended Actions' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Proposed Actions' })).toBeVisible();

    await page.getByTitle('Audit Log').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /Action Audit Log/i })).toBeVisible();
    await expect(page.getByTestId('audit-log-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Recent Audit Events' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Tool Registry' })).toBeVisible();

    await page.getByTitle('Logs').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { level: 1, name: /Logs/i })).toBeVisible();
    await expect(page.getByTestId('logs-page')).toBeVisible();

    expect(runtimeErrors).toEqual([]);
    expect(consoleErrors).toEqual([]);
    expect(requestFailures).toEqual([]);
  });
});
