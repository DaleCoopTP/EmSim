import { expect, test, type Page } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

// ADR-038, with e2e's own policy (run.mjs: three wrong passwords lock an
// account, a newly created admin must change the password): an admin
// creates another admin; three wrong passwords lock that account (even the
// right password is then refused); the first admin sees the lock and
// unlocks it; the second admin signs in, is confined to the password-change
// screen, changes the password and reaches the workspace.
async function signIn(page: Page, baseURL: string, login: string, password: string) {
  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill(login);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
}

test("admin login policy: lock, unlock, forced password change", async ({ page }) => {
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });

  await signIn(page, baseURL, "admin", bootstrapPassword);
  await page.getByRole("link", { name: "Пользователи" }).click();
  await page.getByRole("button", { name: "Создать пользователя" }).click();
  const form = page.locator("form.user-form");
  await form.getByLabel("Логин").fill("policy-admin");
  await form.getByLabel("Пароль", { exact: true }).fill("temporary-pass-1");
  await form.getByLabel("ФИО").fill("Проверка политики");
  await form.locator("select").first().selectOption("admin");
  await form.getByRole("button", { name: "Создать" }).click();
  const row = page.locator("tbody tr", { hasText: "policy-admin" });
  await expect(row).toContainText("Ждёт смены пароля");
  await page.getByRole("button", { name: "Выйти" }).click();

  for (let i = 0; i < 3; i++) {
    await signIn(page, baseURL, "policy-admin", "wrong-password-xx");
    await expect(page.getByRole("alert")).toContainText("Неверный логин или пароль");
  }
  await signIn(page, baseURL, "policy-admin", "temporary-pass-1");
  await expect(page.getByRole("alert")).toContainText("заблокирована");

  await signIn(page, baseURL, "admin", bootstrapPassword);
  await page.getByRole("link", { name: "Пользователи" }).click();
  await expect(row).toContainText("Заблокирован до");
  await row.getByRole("button", { name: "Разблокировать" }).click();
  await expect(row).not.toContainText("Заблокирован до");
  await expect(row).toContainText("Ждёт смены пароля");
  await page.getByRole("button", { name: "Выйти" }).click();

  await signIn(page, baseURL, "policy-admin", "temporary-pass-1");
  await expect(page.getByRole("heading", { name: "Смена пароля" })).toBeVisible();
  await page.getByLabel("Текущий пароль").fill("not-the-password");
  await page.getByLabel("Новый пароль", { exact: true }).fill("a-much-better-pass");
  await page.getByLabel("Повторите новый пароль").fill("a-much-better-pass");
  await page.getByRole("button", { name: "Сменить пароль" }).click();
  await expect(page.getByRole("alert")).toContainText("Текущий пароль указан неверно");
  await page.getByLabel("Текущий пароль").fill("temporary-pass-1");
  await page.getByRole("button", { name: "Сменить пароль" }).click();
  await expect(page.getByRole("link", { name: "Пользователи" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Смена пароля" })).toHaveCount(0);
});
