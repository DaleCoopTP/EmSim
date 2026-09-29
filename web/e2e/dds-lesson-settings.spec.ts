import { expect, request as apiRequest, test, type APIResponse } from "@playwright/test";

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080";

async function ok(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

// See operator112.spec.ts's own adminLogin: retry past the login limiter's
// fixed window instead of treating a full-suite 429 as flakiness.
async function adminLogin(admin: Awaited<ReturnType<typeof apiRequest.newContext>>) {
  for (let attempt = 0; ; attempt++) {
    const response = await admin.post("/api/v1/auth/login", { data: { login: "admin", password: bootstrapPassword } });
    if (response.status() !== 429 || attempt >= 14) return ok(response);
    await new Promise((resolve) => setTimeout(resolve, 5_000));
  }
}

// ДДС-6/ADR-035's own e2e: an instructor creates a DDS lesson with a
// non-default norm, gives it its own weights and pass threshold, fills the
// queue at random and starts it — and the lesson page shows what was frozen.
test("ДДС-6: norm, weights, threshold and random fill on a draft lesson", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1366, height: 900 });
  const admin = await apiRequest.newContext({ baseURL });
  await adminLogin(admin);
  await ok(await admin.put("/api/v1/admin/workstations", { data: [{ number: 915, label: "ДДС-6 настройки" }] }));
  const trainee = await (await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-dds6-trainee", password, full_name: "Обучаемый ДДС-6", role: "trainee", service_code: "dds_district_chertanovo",
  } }))).json();
  await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-dds6-instructor", password, full_name: "Преподаватель ДДС-6", role: "instructor",
  } }));
  await admin.dispose();

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("e2e-dds6-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();

  // --- create with a 45 s primary norm ---
  await page.getByRole("button", { name: "Создать занятие" }).click();
  await page.getByLabel("Название").fill("ДДС-6 настройки E2E");
  await page.getByLabel("Упражнение").selectOption("dds_processing");
  // A primary norm below 10 s is refused before any request; it runs from
  // the card's opening, so it may be shorter than the opening norm.
  await page.getByLabel(/Первичное решение/).fill("5");
  await expect(page.getByRole("alert")).toContainText("Первичное решение");
  await expect(page.getByRole("button", { name: "Создать", exact: true })).toBeDisabled();
  await page.getByLabel(/Первичное решение/).fill("45");
  await page.getByRole("button", { name: "Создать", exact: true }).click();
  await expect(page.getByRole("heading", { name: "ДДС-6 настройки E2E" })).toBeVisible();
  await expect(page.locator(".lesson-meta")).toContainText("30 / 45 / 180 с");

  // --- pass threshold (weights belong to the scenario, not the lesson) ---
  await expect(page.getByRole("heading", { name: "Порог зачёта" })).toBeVisible();
  await expect(page.getByRole("spinbutton", { name: /^Вес:/ })).toHaveCount(0);
  await page.getByLabel(/Порог зачёта, баллов/).fill("85");
  await page.getByRole("button", { name: "Сохранить порог" }).click();
  await expect(page.getByLabel(/Порог зачёта, баллов/)).toHaveValue("85");

  // --- random fill, then the ordinary save and start ---
  const row = page.locator(".assignment-row").first();
  await row.getByLabel("Рабочее место").selectOption("915");
  await row.getByLabel("Обучаемый").selectOption(trainee.id);
  await page.getByLabel(/Раздел 14/).check();
  await page.getByLabel("Карточек на РМ").fill("1");
  await page.getByRole("button", { name: "Заполнить случайно" }).click();
  await expect(row.getByText(/Сохранено кейсов: 1/)).toBeVisible();
  await page.getByRole("button", { name: "Сохранить назначения" }).click();
  await expect(page.getByText("Назначения сохранены.")).toBeVisible();
  await page.getByRole("button", { name: /Запустить занятие/ }).click();
  await expect(page.locator(".lesson-heading .status-badge")).toHaveText("Идёт");

  // --- started: the norm and scoring are read-only ---
  await expect(page.getByRole("button", { name: "Сохранить порог" })).toHaveCount(0);
  await expect(page.locator(".lesson-scoring-settings")).toContainText("85");
});
