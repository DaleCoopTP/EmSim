import { expect, test } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

// ADR-038: the "Конфигурация" screen shows the api's and the worker's
// effective settings for reading only — no field to type into, no secret.
test("admin config: effective settings, read-only, no credentials", async ({ page }) => {
  test.setTimeout(120_000);
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("admin");
  await page.getByLabel("Пароль").fill(bootstrapPassword);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "Конфигурация" }).click();

  await expect(page.getByRole("heading", { name: "Конфигурация" })).toBeVisible();
  await expect(page.locator(".status-panel", { hasText: "api" }).getByText("SESSION_TTL")).toBeVisible();
  // The worker reports its settings within a probe round (30 s).
  await expect(page.locator(".status-panel", { hasText: "worker " }).getByText("BACKUP_KEEP")).toBeVisible({ timeout: 60_000 });
  await expect(page.locator("main input, main select, main textarea")).toHaveCount(0);
  await expect(page.locator("main")).not.toContainText("postgres://");
  await expect(page.locator("main")).not.toContainText("@postgres");
});
