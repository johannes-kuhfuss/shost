const { test, expect } = require('@playwright/test');

test('readiness countdown completes offline and the page shows recovery', async ({ page, context, request }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/probes');
  await page.getByLabel('Disable readiness for (seconds)').fill('4');
  await page.getByRole('button', { name: 'Temporarily disable readiness' }).click();
  const countdown = page.locator('#readiness-countdown');
  await expect(countdown).toContainText('seconds remaining');
  expect((await request.get('/health/ready')).status()).toBe(503);

  // Model losing Service access, without interrupting the server's deadline.
  await context.setOffline(true);
  expect(await page.evaluate(async () => {
    try { await fetch('/health/ready'); return false; } catch { return true; }
  })).toBe(true);
  await expect(countdown).toContainText('The readiness pause has ended.', { timeout: 10000 });
  await expect.poll(async () => (await request.get('/health/ready')).status()).toBe(200);

  await context.setOffline(false);
  await page.getByRole('link', { name: 'Refresh probe status' }).click();
  await expect(page.getByRole('button', { name: 'Temporarily disable readiness' })).toBeVisible();
  await expect(countdown).toHaveCount(0);
  expect(errors).toEqual([]);
});
