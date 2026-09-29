import { expect, test } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

// ADR-033: the administrator's "Состояние" screen — schema, worker,
// queue and backups — and a manual backup that the worker completes and
// the screen then lists. The stack's own daily backup may already have
// run at startup, so the check is "one more copy than before", not a
// fixed count.
test("admin status: worker heartbeat, manual backup appears in the list", async ({ page }) => {
  test.setTimeout(120_000);
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("admin");
  await page.getByLabel("Пароль").fill(bootstrapPassword);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "Состояние" }).click();

  await expect(page.getByRole("heading", { name: "Состояние" })).toBeVisible();
  const schemaCard = page.locator(".status-card", { hasText: "База данных" });
  await expect(schemaCard).toHaveClass(/status-ok/);
  await expect(page.locator(".status-card", { hasText: "Worker" })).toHaveClass(/status-ok/, { timeout: 60_000 });

  // ADR-038: the load panel and the build version are on the same screen.
  await expect(page.getByRole("heading", { name: "Нагрузка" })).toBeVisible();
  await expect(page.locator(".status-card", { hasText: "Сеансы и потоки" })).toBeVisible();
  await expect(page.getByText(/версия \S+/).first()).toBeVisible();

  const startButton = page.getByRole("button", { name: "Создать копию сейчас" });
  await expect(startButton).toBeEnabled({ timeout: 60_000 });
  const copies = page.locator(".status-panel", { hasText: "Резервные копии" }).locator("tbody tr");
  const before = await copies.count();
  await startButton.click();
  await expect(copies).toHaveCount(before + 1, { timeout: 60_000 });
  await expect(startButton).toBeEnabled();
  await expect(page.getByText("Каталог копий не настроен")).toHaveCount(0);
});
