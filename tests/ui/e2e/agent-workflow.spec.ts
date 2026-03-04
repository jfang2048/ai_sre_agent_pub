import { expect, Page, test } from '@playwright/test';

async function seedIncident(page: Page, alertID: string): Promise<void> {
  const response = await page.request.post('/api/v1/incidents/alerts', {
    data: {
      id: alertID,
      title: 'Checkout API error burst alert',
      service: 'checkout',
      severity: 'critical',
      starts_at: new Date().toISOString(),
      labels: {
        service: 'checkout',
        commit: 'abc123',
        deployment: 'checkout-v2026.02.26',
      },
      annotations: {
        summary: '5xx and timeout burst after rollout',
        runbook: 'configs/agent_playbooks.yaml#high-cpu',
      },
    },
  });
  expect(response.status()).toBe(200);
}

async function waitForIncident(page: Page, alertID: string): Promise<void> {
  for (let i = 0; i < 30; i += 1) {
    const response = await page.request.get('/api/v1/agent/incidents?limit=20');
    if (response.status() === 200) {
      const payload = await response.json();
      const incidents = Array.isArray(payload?.incidents) ? payload.incidents : [];
      if (incidents.some((incident: { alert_id?: string }) => incident.alert_id === alertID)) {
        return;
      }
    }
    await page.waitForTimeout(500);
  }
  throw new Error(`incident ${alertID} was not propagated to /api/v1/agent/incidents in time`);
}

test.describe('Agent Incident Workflow', () => {
  test('renders incident workflow output and executes guarded action', async ({ page }) => {
    const alertID = `ui-e2e-${Date.now()}`;
    await seedIncident(page, alertID);
    await waitForIncident(page, alertID);

    const consoleErrors: string[] = [];
    const runtimeErrors: string[] = [];
    const failedAgentAPI: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });
    page.on('pageerror', (error) => runtimeErrors.push(String(error)));
    page.on('response', (response) => {
      const url = response.url();
      if (!url.includes('/api/v1/agent/') && !url.includes('/api/v1/incidents/')) {
        return;
      }
      if (response.status() >= 400) {
        failedAgentAPI.push(`${response.status()} ${url}`);
      }
    });

    await page.goto('/');
    await page.getByTitle('AGENT').click();
    await page.waitForLoadState('networkidle');

    await expect(page.getByRole('heading', { name: /AGENT Operations/i })).toBeVisible();
    const workflowSection = page.getByTestId('agent-incident-workflow');
    await expect(workflowSection).toBeVisible();

    await workflowSection.locator('select').first().selectOption(alertID);
    await expect(workflowSection.locator('select').first()).toHaveValue(alertID);
    await expect(workflowSection.getByText(/Probable root cause:/i)).toBeVisible();
    await expect(workflowSection.getByText(/Workflow Stages/i)).toBeVisible();
    await expect(workflowSection.getByText(/Correlations/i)).toBeVisible();
    await expect(workflowSection.getByText('Evidence', { exact: true })).toBeVisible();
    await expect(workflowSection.getByText(/Recommended next steps/i)).toBeVisible();
    await expect(workflowSection.getByText(/Guarded automation/i)).toBeVisible();
    await expect(workflowSection.getByText(/Action audit trail/i)).toBeVisible();

    const executeButton = workflowSection.getByRole('button', { name: /Execute/i }).first();
    await expect(executeButton).toBeVisible();
    await executeButton.click();
    await expect(page.getByText(/Incident action .* (executed|dry_run|blocked):/i)).toBeVisible();

    const rollbackButton = workflowSection.getByRole('button', { name: /Rollback/i }).first();
    await expect(rollbackButton).toBeVisible();
    await rollbackButton.click();
    await expect(page.getByText(/Rollback .* (rolled_back|dry_run|blocked):/i)).toBeVisible();

    expect(runtimeErrors).toEqual([]);
    expect(consoleErrors).toEqual([]);
    expect(failedAgentAPI).toEqual([]);
  });
});
