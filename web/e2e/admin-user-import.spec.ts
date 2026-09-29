import { expect, test } from "@playwright/test";

const bootstrapPassword = "local-only-admin-password";

test("admin user import: refused file, dry run, creation, password sheet", async ({ page }) => {
  const baseURL = process.env.E2E_BASE_URL;
  if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("admin");
  await page.getByLabel("Пароль").fill(bootstrapPassword);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "Пользователи" }).click();
  await page.getByRole("button", { name: "Загрузить из CSV" }).click();

  const panel = page.locator(".user-import");
  const file = (text: string) => ({ name: "class.csv", mimeType: "text/csv", buffer: Buffer.from(text, "utf-8") });

  await panel.getByLabel("Файл CSV").setInputFiles(file("login;full_name;role\ncsv-one;Иванов Иван;trainee\nbad login!;Петров;trainee\n"));
  await panel.getByRole("button", { name: "Проверить" }).click();
  await expect(panel.getByRole("alert")).toContainText("строка 2 — логин: недопустимое значение");
  await expect(panel.getByRole("button", { name: "Создать пользователей" })).toBeDisabled();

  await panel.getByLabel("Файл CSV").setInputFiles(file("login;full_name;role\ncsv-one;Иванов Иван;trainee\ncsv-two;Петров Пётр;trainee\n"));
  await panel.getByRole("button", { name: "Проверить" }).click();
  await expect(panel.getByText("будет создано пользователей — 2")).toBeVisible();
  await panel.getByRole("button", { name: "Создать пользователей" }).click();

  await expect(panel.getByText("Пароли показаны один раз")).toBeVisible();
  const passwords = panel.locator("tbody code");
  await expect(passwords).toHaveCount(2);
  const first = (await passwords.first().textContent()) ?? "";
  expect(first.length).toBeGreaterThanOrEqual(14);
  const downloadPromise = page.waitForEvent("download");
  await panel.getByRole("button", { name: /Скачать лист/ }).click();
  expect((await downloadPromise).suggestedFilename()).toBe("new-users.csv");

  await panel.getByRole("button", { name: "Закрыть" }).click();
  await expect(page.locator("tbody tr", { hasText: "csv-one" })).toBeVisible();
  await expect(page.locator("tbody tr", { hasText: "csv-two" })).toBeVisible();
});
