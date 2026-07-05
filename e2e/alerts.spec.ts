import { test, expect } from '@playwright/test';

// Erwartet laufenden query-Service (ClickHouse) mit Daten + Login.
test.describe('rocketplaneIO — Alerts (M2)', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/auth/login', {
      data: { email: 'demo@rocketplane.io', password: 'rocketplane' },
    });
  });

  test('Alerts-Seite wertet Regeln live aus', async ({ page }) => {
    await page.goto('/alerts');
    await expect(page.getByRole('heading', { name: 'Alerts' })).toBeVisible();
    // Firing-Summary „N firing"
    await expect(page.getByText(/\d+ firing/)).toBeVisible();
    // ein bekannter Regelname
    await expect(page.getByText(/checkout-api error rate high/)).toBeVisible();
  });

  test('Alert verlinkt zum betroffenen Service', async ({ page }) => {
    await page.goto('/alerts');
    const link = page.getByRole('link', { name: 'View service' }).first();
    await expect(link).toBeVisible();
    await link.click();
    await expect(page).toHaveURL(/\/services\/[a-z-]+/);
  });

  test('Screenshot: Alerts', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/alerts');
    await expect(page.getByText(/\d+ firing/)).toBeVisible();
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/screenshots/alerts.png' });
  });
});
