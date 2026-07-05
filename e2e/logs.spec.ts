import { test, expect } from '@playwright/test';

// Erwartet laufenden query-Service (ClickHouse) mit Logs + Login.
test.describe('rocketplaneIO — Logs-Explorer & Korrelation (M1)', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/auth/login', {
      data: { email: 'demo@rocketplane.io', password: 'rocketplane' },
    });
  });

  test('Logs-Seite streamt Live-Logs mit Trace-Links', async ({ page }) => {
    await page.goto('/logs');
    await expect(page.getByRole('heading', { name: 'Logs' })).toBeVisible();
    // eine Log-Zeile mit einem Body aus dem Generator
    await expect(page.getByText(/started|failed/).first()).toBeVisible();
    // Trace-Link (8 hex) ist vorhanden
    await expect(page.getByRole('link', { name: /^[0-9a-f]{8}$/ }).first()).toBeVisible();
  });

  test('Trace-Detail zeigt Related Logs (korreliert)', async ({ page }) => {
    // einen Error-Trace über die Trace-Liste öffnen (hat garantiert Logs)
    await page.goto('/traces?service=checkout-api');
    await page.getByRole('link', { name: /POST \/checkout/ }).first().click();
    await expect(page.getByText('duration')).toBeVisible();
    await expect(page.getByText('related logs')).toBeVisible();
  });

  test('Log-Zeile verlinkt zum Trace', async ({ page }) => {
    await page.goto('/logs');
    const link = page.getByRole('link', { name: /^[0-9a-f]{8}$/ }).first();
    await expect(link).toBeVisible();
    await link.click();
    await expect(page).toHaveURL(/\/traces\/[0-9a-f]{32}/);
  });

  test('Screenshot: Logs-Seite + Related Logs im Trace', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/logs');
    await expect(page.getByRole('heading', { name: 'Logs' })).toBeVisible();
    await expect(page.getByText(/started|failed/).first()).toBeVisible();
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/screenshots/logs-list.png' });

    await page.goto('/traces?service=checkout-api');
    await page.getByRole('link', { name: /POST \/checkout/ }).first().click();
    await expect(page.getByText('related logs')).toBeVisible();
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/screenshots/trace-related-logs.png' });
  });
});
