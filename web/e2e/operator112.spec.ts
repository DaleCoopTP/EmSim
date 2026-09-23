import { expect, request as apiRequest, test, type APIResponse } from "@playwright/test";

const password = "e2e-password-123";
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080";

async function ok(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

test("operator 112: instructor assignment → incoming call → saved draft → dispatch → manual review", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  const admin = await apiRequest.newContext({ baseURL });
  await ok(await admin.post("/api/v1/auth/login", { data: { login: "admin", password: "local-only-admin-password" } }));
  await ok(await admin.put("/api/v1/admin/workstations", { data: [{ number: 902, label: "112 browser workstation" }] }));
  const trainee = await (await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-112-trainee", password, full_name: "Обучаемый 112", role: "trainee",
  } }))).json();
  await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-112-instructor", password, full_name: "Преподаватель 112", role: "instructor",
  } }));
  await admin.dispose();

  const instructorAPI = await apiRequest.newContext({ baseURL });
  await ok(await instructorAPI.post("/api/v1/auth/login", { data: { login: "e2e-112-instructor", password } }));
  const catalogue = await (await ok(await instructorAPI.get("/api/v1/scenarios?status=approved&exercise_type=operator112_intake&page=1&page_size=20"))).json();
  const scenario = catalogue.items.find((candidate: { source_key?: string }) => candidate.source_key === "pilot-112-medical-01");
  expect(scenario).toBeTruthy();
  await instructorAPI.dispose();

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("e2e-112-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();
  await page.getByRole("button", { name: "Создать занятие" }).click();
  await page.getByLabel("Название").fill("Первый вызов 112 E2E");
  await page.getByLabel("Упражнение").selectOption("operator112_intake");
  await page.getByRole("button", { name: "Создать", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Первый вызов 112 E2E" })).toBeVisible();
  const lessonId = new URL(page.url()).pathname.split("/").at(-1)!;
  await page.getByLabel("Рабочее место").selectOption("902");
  await page.getByLabel("Обучаемый").selectOption(trainee.id);
  await page.locator(".queue-editor select").selectOption(scenario.id);
  await page.getByRole("button", { name: "+ В очередь" }).click();
  await page.getByRole("button", { name: "Сохранить назначения" }).click();
  await expect(page.getByText("Назначения сохранены.")).toBeVisible();
  await page.getByRole("button", { name: /Запустить занятие/ }).click();
  await expect(page.locator(".lesson-heading .status-badge")).toHaveText("Идёт");
  await page.getByRole("button", { name: "Выйти" }).click();

  await page.getByLabel("Логин").fill("e2e-112-trainee");
  await page.getByLabel("Пароль").fill(password);
  await page.getByLabel("Номер рабочего места (для обучаемого)").fill("902");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Первый вызов 112 E2E" })).toBeVisible();
  const offered = await (await ok(await page.request.get("/api/v1/my/items"))).json();
  expect(offered).toHaveLength(1);
  const beforeAnswer = await (await ok(await page.request.get(`/api/v1/items/${offered[0].id}`))).json();
  expect(beforeAnswer.intake_reference).toBeUndefined();
  expect(beforeAnswer.intake_state.transcript).toHaveLength(0);
  expect(JSON.stringify(beforeAnswer)).not.toContain("Здравствуйте. Мне 19 лет.");
  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await expect(page.getByText("+79161313131")).toBeVisible();
  await expect(page.getByText("Здравствуйте. Мне 19 лет.", { exact: false })).toHaveCount(0);
  await page.getByRole("button", { name: "Открыть вызов" }).click();
  await page.getByRole("button", { name: "Ответить" }).click();
  await expect(page.getByText(/Здравствуйте\. Мне 19 лет/)).toBeVisible();
  await page.getByLabel("Кем приходится пострадавшему").selectOption("known");
  await page.getByLabel("Возраст").selectOption("known");
  await page.getByLabel("Возраст: значение").fill("19");
  await page.getByLabel("Город").selectOption("known");
  await page.getByLabel("Город: значение").fill("Москва");
  await page.getByLabel("Улица").selectOption("known");
  await page.getByLabel("Улица: значение").fill("улица Космонавтов");
  await page.getByLabel("Дом").selectOption("known");
  await page.getByLabel("Дом: значение").fill("2");
  await page.getByLabel("Корпус").selectOption("known");
  await page.getByLabel("Корпус: значение").fill("3");
  await page.getByLabel("Ориентир").selectOption("known");
  await page.getByLabel("Ориентир: значение").fill("метро ВДНХ");
  await page.getByLabel("Тип обращения").selectOption("known");
  await page.getByLabel("Жалобы").selectOption("known");
  await page.getByLabel("Жалобы: значение").fill("сильная диарея и обильная рвота");
  await page.getByLabel("Есть пострадавшие").selectOption("known");
  await page.getByLabel("Число пострадавших").selectOption("known");
  await page.getByLabel("Число пострадавших: значение").fill("1");
  await page.getByRole("button", { name: "Сохранить карточку" }).click();
  await expect(page.getByText("Черновик сохранён на сервере.")).toBeVisible();

  await page.reload();
  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await expect(page.getByLabel("Возраст: значение")).toHaveValue("19");
  await expect(page.getByText(/Здравствуйте\. Мне 19 лет/)).toBeVisible();
  await page.getByRole("button", { name: "Направить сохранённую карточку" }).click();
  await expect(page.getByText(/Направлена в pilot_ambulance/)).toBeVisible();
  await page.getByRole("button", { name: "Завершить разговор" }).click();
  await page.getByRole("button", { name: "Завершить обработку" }).click();
  await expect(page.getByText(/Результат появится после оценки преподавателя/)).toBeVisible();
  await page.getByRole("button", { name: "Выйти" }).click();

  await page.getByLabel("Логин").fill("e2e-112-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();
  await page.goto(`${baseURL}/instructor/lessons/${lessonId}/assessments`);
  await expect(page.getByText("Ожидает преподавателя")).toBeVisible();
  await page.getByRole("link", { name: "Открыть →" }).click();
  await expect(page.getByRole("heading", { name: "Итоговая карточка" })).toBeVisible();
  await expect(page.locator(".intake-review-card").first().getByText(/метро ВДНХ/)).toBeVisible();
  await expect(page.getByText(/Адресат: pilot_ambulance/)).toBeVisible();
  const statuses = page.locator("form.lesson-form tbody select");
  await statuses.nth(0).selectOption("partial");
  await statuses.nth(1).selectOption("met");
  await statuses.nth(2).selectOption("met");
  await page.getByLabel("Причина").fill("Карточка заполнена частично, передача выполнена");
  await page.getByRole("button", { name: "Сохранить экспертную оценку" }).click();
  await expect(page.getByText(/итог: ready · 80\.0/)).toBeVisible();
  await page.getByRole("button", { name: "Выйти" }).click();
  await page.getByLabel("Логин").fill("e2e-112-trainee");
  await page.getByLabel("Пароль").fill(password);
  await page.getByLabel("Номер рабочего места (для обучаемого)").fill("902");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "История" }).click();
  await expect(page.getByRole("heading", { name: "Моя история" })).toBeVisible();
  await expect(page.getByRole("table").last().getByText("80.0")).toBeVisible();
  const results = await (await ok(await page.request.get("/api/v1/my/results?exercise_type=operator112_intake"))).json();
  expect(results).toHaveLength(1);
  expect(JSON.stringify(results)).not.toContain("intake_reference");
});
