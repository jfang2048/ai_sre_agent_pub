import { test, expect } from '@playwright/test';

test.describe('GPU Observability', () => {
  test('gpu observability page renders and gpu APIs respond', async ({ page }) => {
    await page.goto('/');

    await page.getByTitle('GPU Observability').click();
    await expect(page.getByTestId('gpu-observability-page')).toBeVisible();
    await expect(page.getByText('GPU Metric Timeline')).toBeVisible();
    await expect(page.getByText('GPU Event Timeline')).toBeVisible();

    const nodesResp = await page.request.get('/api/v1/gpu/nodes');
    expect(nodesResp.status()).toBe(200);

    const nodesJson = await nodesResp.json();
    const nodes = Array.isArray(nodesJson?.nodes) ? nodesJson.nodes : [];

    if (nodes.length > 0) {
      const collectorId = nodes[0]?.collector_id;
      const gpuMap = nodes[0]?.gpus ?? {};
      const gpuId = Object.keys(gpuMap)[0] ?? '0';

      const timelineResp = await page.request.get(`/api/v1/gpu/timeline?collector_id=${encodeURIComponent(collectorId)}&gpu_id=${encodeURIComponent(gpuId)}&metric=node_gpu_utilization_sm_percent&window=30m`);
      expect(timelineResp.status()).toBe(200);

      const eventsResp = await page.request.get(`/api/v1/gpu/events?collector_id=${encodeURIComponent(collectorId)}&gpu_id=${encodeURIComponent(gpuId)}&window=30m`);
      expect(eventsResp.status()).toBe(200);

      const corrResp = await page.request.get(`/api/v1/gpu/correlation?collector_id=${encodeURIComponent(collectorId)}`);
      expect(corrResp.status()).toBe(200);
    }
  });
});
