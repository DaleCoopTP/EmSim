import { expect, test } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

// ADR-038: the administrator's "Отчёты" screen — usage totals and the
// per-day chart after a login, the failures tab, and both CSV downloads.
test("admin reports: usage, failures, CSV", async ({ page }) => {
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("admin");
  await page.getByLabel("Пароль").fill(bootstrapPassword);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "Отчёты" }).click();

  await expect(page.getByRole("heading", { name: "Отчёты" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Итоги за период" })).toBeVisible();
  await expect(page.locator(".usage-totals dt", { hasText: "Входы" })).toBeVisible();
  await expect(page.locator(".usage-chart")).toBeVisible();

  let download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Скачать CSV" }).click();
  expect((await download).suggestedFilename()).toBe("usage.csv");

  await page.getByRole("tab", { name: "Сбои" }).click();
  await expect(page.getByRole("heading", { name: "Упавшие фоновые задачи" })).toBeVisible();
  download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Скачать CSV" }).click();
  expect((await download).suggestedFilename()).toBe("failures.csv");
});
