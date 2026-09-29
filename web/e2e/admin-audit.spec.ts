import { expect, test } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

// ADR-038: the administrator's "Журнал" screen. A wrong-password attempt
// and a normal login both land in the log; the filter narrows it, the
// rows name the actor by login, and the CSV export downloads.
test("admin audit: rows, filter by outcome, CSV download", async ({ page, browser }) => {
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });

  // A rejected login from a second, anonymous context.
  const other = await browser.newContext({ viewport: { width: 1366, height: 768 } });
  const stranger = await other.newPage();
  await stranger.goto(`${baseURL}/login`);
  await stranger.getByLabel("Логин").fill("admin");
  await stranger.getByLabel("Пароль").fill("definitely-not-the-password");
  await stranger.getByRole("button", { name: "Войти" }).click();
  await other.close();

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("admin");
  await page.getByLabel("Пароль").fill(bootstrapPassword);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "Журнал" }).click();

  await expect(page.getByRole("heading", { name: "Журнал" })).toBeVisible();
  const rows = page.locator(".admin-audit tbody tr");
  await expect(rows.filter({ hasText: "Вход" }).first()).toBeVisible();
  await expect(rows.filter({ hasText: "admin (admin)" }).first()).toBeVisible();

  await page.getByLabel("Результат").selectOption("rejected");
  await page.getByRole("button", { name: "Показать" }).click();
  await expect(rows.first()).toContainText("отклонено");
  await expect(rows.filter({ hasText: "выполнено" })).toHaveCount(0);

  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("link", { name: "Скачать CSV" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("audit.csv");
});
