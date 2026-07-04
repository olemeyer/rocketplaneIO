import { test, expect } from '@playwright/test';

// Erwartet laufenden query-Service (ClickHouse) mit Daten + Login.
test.describe('rocketplaneIO — Traces-Explorer (M1)', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/auth/login', {
      data: { email: 'demo@rocketplane.io', password: 'rocketplane' },
    });
  });

  test('Traces-Liste zeigt gefilterte Traces', async ({ page }) => {
    await page.goto('/traces');
    await expect(page.getByRole('heading', { name: 'Traces' })).toBeVisible();
    // mindestens eine Trace-Zeile mit einem Root-Namen
    await expect(page.getByRole('link', { name: /POST \/checkout|GET \/cart|POST \/charge/ }).first()).toBeVisible();
  });

  test('Service-Health-Card verlinkt gefiltert in die Traces', async ({ page }) => {
    await page.goto('/explore');
    // Die Health-Card ist ein Link zu /traces?service=…
    const card = page.getByRole('link', { name: /checkout-api/ }).first();
    await expect(card).toBeVisible();
    await card.click();
    await expect(page).toHaveURL(/\/traces\?service=checkout-api/);
    await expect(page.getByRole('heading', { name: 'Traces' })).toBeVisible();
  });

  test('Klick auf eine Trace-Zeile öffnet das Waterfall-Detail', async ({ page }) => {
    await page.goto('/traces?service=checkout-api');
    const row = page.getByRole('link', { name: /POST \/checkout/ }).first();
    await row.click();
    await expect(page).toHaveURL(/\/traces\/[0-9a-f]{32}/);
    await expect(page.getByText('duration')).toBeVisible();
    await expect(page.getByText('POST /checkout').first()).toBeVisible();
  });

  test('Screenshot: Traces-Liste + Trace-Detail', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/traces');
    await expect(page.getByRole('heading', { name: 'Traces' })).toBeVisible();
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/screenshots/traces-list.png' });

    await page.getByRole('link', { name: /POST \/checkout|GET \/cart|POST \/charge/ }).first().click();
    await expect(page.getByText('duration')).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: 'e2e/screenshots/trace-detail.png' });
  });
});
