import { test, expect } from '@playwright/test';

const DEMO = { email: 'demo@rocketplane.io', password: 'rocketplane' };

test.describe('rocketplane — Authentifizierung', () => {
  test('unauthentifiziertes /explore leitet auf /login um', async ({ page }) => {
    await page.goto('/explore');
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  });

  test('Demo-Login führt in die App', async ({ page }) => {
    await page.goto('/login');
    await page.getByRole('button', { name: 'Continue with the demo workspace' }).click();
    await expect(page).toHaveURL(/\/explore/);
    // echte Daten sind da -> Auth-Gate für den API-Proxy funktioniert mit Session
    await expect(page.getByText('payment-gateway', { exact: true })).toBeVisible();
  });

  test('Sign out führt zurück zum Login', async ({ page }) => {
    await page.request.post('/api/auth/login', { data: DEMO });
    await page.goto('/explore');
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page).toHaveURL(/\/login/);
  });

  test('Screenshot der Login-Seite', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/login');
    await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
    await page.waitForTimeout(400);
    await page.screenshot({ path: 'e2e/screenshots/login-dark.png' });
  });
});
