import { expect, test } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

// ADR-038: the administrator switches maintenance mode on the status
// screen; the banner then shows on every screen with the note, and it goes
// away when the mode is switched off. (That a lesson start is actually
// refused is covered by the API integration test.)
test("admin maintenance: switch on shows the banner, switch off removes it", async ({ page }) => {
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("admin");
  await page.getByLabel("Пароль").fill(bootstrapPassword);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "Состояние" }).click();

  const panel = page.locator(".status-panel", { hasText: "Режим обслуживания" });
  await expect(page.locator(".maintenance-banner")).toHaveCount(0);
  await panel.getByPlaceholder("Например: обновление до 15:00").fill("Обновление до 15:00");
  await panel.getByRole("button", { name: "Включить" }).click();

  await expect(panel.getByRole("button", { name: "Выключить" })).toBeVisible();
  await expect(page.locator(".maintenance-banner")).toContainText("Технические работы");
  await expect(page.locator(".maintenance-banner")).toContainText("Обновление до 15:00");
  // Another screen, same banner.
  await page.getByRole("link", { name: "Пользователи" }).click();
  await expect(page.locator(".maintenance-banner")).toBeVisible();

  await page.getByRole("link", { name: "Состояние" }).click();
  await panel.getByRole("button", { name: "Выключить" }).click();
  await expect(panel.getByRole("button", { name: "Включить" })).toBeVisible();
  await expect(page.locator(".maintenance-banner")).toHaveCount(0);
});
