import { expect, request as apiRequest, test, type APIRequestContext, type APIResponse } from "@playwright/test";

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080";

async function ok(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

// Every e2e spec's own setup logs into the same "admin" account, against
// RFC-001 §9's real 5-attempts/min-per-login limiter (never relaxed for
// tests, internal/auth/ratelimit.go). A fast full-suite run can stack more
// admin logins than that into one minute; retry past the limiter's fixed
// window instead of treating its 429 as spurious flakiness.
async function adminLogin(admin: APIRequestContext) {
  for (let attempt = 0; ; attempt++) {
    const response = await admin.post("/api/v1/auth/login", { data: { login: "admin", password: bootstrapPassword } });
    if (response.status() !== 429 || attempt >= 14) return ok(response);
    await new Promise((resolve) => setTimeout(resolve, 5_000));
  }
}

test("operator 112: instructor assignment → incoming call → saved draft → dispatch → manual review", async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  const admin = await apiRequest.newContext({ baseURL });
  await adminLogin(admin);
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
  const catalogue = await (await ok(await instructorAPI.get("/api/v1/scenarios?status=archived&exercise_type=operator112_intake&page=1&page_size=20"))).json();
  const scenario = catalogue.items.find((candidate: { source_key?: string }) => candidate.source_key === "pilot-112-medical-01");
  expect(scenario).toBeTruthy();
  const scenarioDetail = await (await ok(await instructorAPI.get(`/api/v1/scenarios/${scenario.id}`))).json();
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
  // Archived regression fixture is assignable by its approved version ID,
  // while the instructor's normal queue selector shows only active cases.
  await ok(await page.request.put(`/api/v1/lessons/${lessonId}/assignments`, { data: [{
    workstation_no: 902, user_id: trainee.id, scenario_version_ids: [scenarioDetail.version_id],
  }] }));
  await page.reload();
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
  expect(JSON.stringify(beforeAnswer)).not.toContain("Дом 2, корпус 3");
  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await expect(page.getByText("+79161313131")).toBeVisible();
  await expect(page.getByText("Дом 2, корпус 3", { exact: false })).toHaveCount(0);
  const availability = page.getByRole("button", { name: "Статус телефонии" });
  await expect(availability).toHaveText("Доступен");
  await availability.click();
  await expect(availability).toHaveText("Недоступен");
  await page.reload();
  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await expect(availability).toHaveText("Недоступен");
  await availability.click();
  await expect(availability).toHaveText("Доступен");
  await page.getByRole("button", { name: "Открыть вызов" }).click();
  await expect(availability).toHaveText("Недоступен");
  await expect(availability).toBeDisabled();
  await page.getByRole("button", { name: "Ответить" }).click();
  await expect(page.getByText(/Здравствуйте\. Мне плохо/)).toBeVisible();
  await expect(page.getByText("Дом 2, корпус 3", { exact: false })).toHaveCount(0);
  await page.getByRole("button", { name: "Сколько вам лет?" }).click();
  await expect(page.getByText("Мне 19 лет.")).toBeVisible();
  await page.getByRole("button", { name: "Назовите адрес, где вы находитесь." }).click();
  await expect(page.getByText(/Номер дома сейчас не вижу/)).toBeVisible();
  await page.getByRole("button", { name: "Удержать" }).click();
  await expect(page.getByText("На удержании", { exact: true })).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await expect(page.getByText("На удержании", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Вернуться к разговору" }).click();
  await page.getByRole("button", { name: "Уточните номер дома, корпус и ориентир." }).click();
  await expect(page.getByText(/Дом 2, корпус 3, рядом с метро ВДНХ/)).toBeVisible();
  await page.getByRole("button", { name: "Сколько человек нуждается в помощи?" }).click();
  await page.getByRole("button", { name: "Какой номер квартиры?" }).click();
  await expect(page.getByText("Номер квартиры не знаю.")).toBeVisible();
  await expect(page.getByText("03 · Скорая помощь")).toBeVisible();
  await page.getByLabel("Статус заявителя: значение").selectOption("victim");
  await page.getByLabel("Канал связи: значение").fill("Телефон, оператор не указан");
  await expect(page.getByLabel("Возраст: значение")).toHaveCount(0);
  await expect(page.getByLabel("Иностранный язык")).toHaveCount(0);
  const address = page.locator(".intake-address");
  expect(await address.evaluate((node) => getComputedStyle(node).overflowY)).toBe("visible");
  expect(await address.evaluate((node) => node.scrollHeight <= node.clientHeight + 1)).toBeTruthy();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  expect(await page.evaluate(() => document.querySelector(".intake-action-bar")!.getBoundingClientRect().top >= document.querySelector(".intake-address")!.getBoundingClientRect().bottom)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath("operator112-layout.png"), fullPage: true });
  await page.getByLabel("Страна: значение").fill("Россия");
  await page.getByLabel("Субъект: значение").fill("Москва");
  await page.getByLabel("Населённый пункт: значение").fill("Москва");
  await page.getByLabel("Улица: значение").fill("улица Космонавтов");
  await page.getByLabel("Дом: значение").fill("2");
  await page.getByLabel("Корпус: значение").fill("3");
  await page.getByLabel("Ориентир: значение").fill("метро ВДНХ");
  await page.getByLabel("Описательный адрес: значение").fill("рядом с метро ВДНХ");
  await page.getByLabel("Жалобы: значение").fill("сильная диарея и обильная рвота");
  await page.getByRole("button", { name: "Пострадавшие", exact: true }).click();
  await page.getByLabel("Число пострадавших: значение").fill("1");
  await page.locator(".intake-console-number").nth(2).getByRole("button", { name: "АОН" }).click();
  await page.getByRole("button", { name: "Сохранить карточку" }).click();
  await expect(page.getByText("Черновик сохранён на сервере.")).toBeVisible();
  const legacyCard = await (await ok(await page.request.get(`/api/v1/items/${offered[0].id}`))).json();
  expect(legacyCard.card.channel.value).toBe("phone");

  await page.reload();
  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await expect(page.getByLabel("Статус заявителя: значение")).toHaveValue("victim");
  await expect(page.getByLabel("Канал связи: значение")).toHaveValue("Телефон, оператор не указан");
  await page.getByLabel("Канал связи: значение").fill("МТС");
  await page.getByRole("button", { name: "Сохранить карточку" }).click();
  await expect(page.getByText("Черновик сохранён на сервере.")).toBeVisible();
  await expect(page.getByLabel("Канал связи: значение")).toHaveValue("МТС");
  await expect(page.getByLabel("Описательный адрес: значение")).toHaveValue("рядом с метро ВДНХ");
  await expect(page.getByLabel("Телефон на место: значение")).toHaveValue("+79161313131");
  await expect(page.getByText("Мне 19 лет.")).toBeVisible();
  await page.getByRole("button", { name: "Направить в 03" }).click();
  await expect(page.getByText(/Направлена в 03/)).toBeVisible();
  await page.getByRole("button", { name: "Завершить разговор" }).click();
  await page.getByRole("button", { name: "Завершить обработку" }).click();
  await expect(availability).toHaveText("Недоступен");
  await expect(availability).toBeDisabled();
  await expect(availability).toHaveText("Доступен", { timeout: 12_000 });
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
  await expect(page.getByRole("heading", { name: "Факты сценария" })).toBeVisible();
  await expect(page.getByText("Карточка сохранена:", { exact: false }).first()).toBeVisible();
  await expect(page.locator(".intake-review-card").first().getByText("Ориентир: метро ВДНХ", { exact: true })).toBeVisible();
  await expect(page.locator(".intake-review-card").first().getByText(/На место: \+79161313131/)).toBeVisible();
  await expect(page.getByText(/Адресат: 03 · Скорая помощь/)).toBeVisible();
  // 112-6/ADR-026: this lesson is created through the UI, so it freezes
  // today's operator112/rubric-v2 (never rubric-v1) — the legacy
  // incoming_call/dispatch_intake route this test exercises never gets
  // an automatic score (operator112_legacy_route), so the expert form's
  // block inputs all start at 0 and every block needs an explicit value,
  // same as ValidateRevision's own base_revision=0 "full set required"
  // rule. Address is only half filled (matches this scenario's own
  // "частично" spirit from before rubric-v2 existed); everything else
  // scores in full; no penalties. Total = 17.5 + 25 + 15 + 7.5 + 7.5 + 10 = 82.5.
  const setPoints = async (title: string, points: number) => {
    await page.locator("form.lesson-form tr", { hasText: title }).locator("input[type=number]").fill(String(points));
  };
  await setPoints("Адрес происшествия", 17.5);
  await setPoints("Профильные карты", 25);
  await setPoints("Темы разговора с заявителем", 15);
  await setPoints("Норматив ответа на вызов", 7.5);
  await setPoints("Норматив заполнения карточки", 7.5);
  await setPoints("Описание со слов заявителя", 10);
  await page.getByLabel("Причина").fill("Карточка заполнена частично, передача выполнена");
  await page.getByRole("button", { name: "Сохранить экспертную оценку" }).click();
  await expect(page.getByText(/экспертная оценка: 82\.5/)).toBeVisible();
  await page.getByRole("button", { name: "Выйти" }).click();
  await page.getByLabel("Логин").fill("e2e-112-trainee");
  await page.getByLabel("Пароль").fill(password);
  await page.getByLabel("Номер рабочего места (для обучаемого)").fill("902");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("link", { name: "История" }).click();
  await expect(page.getByRole("heading", { name: "Моя история" })).toBeVisible();
  await expect(page.getByRole("table").last().getByText("82.5")).toBeVisible();
  const results = await (await ok(await page.request.get("/api/v1/my/results?exercise_type=operator112_intake"))).json();
  expect(results).toHaveLength(1);
  expect(JSON.stringify(results)).not.toContain("intake_reference");
});

test("operator 112: no contact and dropped call close without dispatch", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  const admin = await apiRequest.newContext({ baseURL });
  await adminLogin(admin);
  await ok(await admin.put("/api/v1/admin/workstations", { data: [{ number: 903, label: "112 no contact" }, { number: 904, label: "112 dropped call" }] }));
  const trainees = [];
  for (const login of ["e2e-112-no-contact", "e2e-112-call-dropped"]) {
    trainees.push(await (await ok(await admin.post("/api/v1/admin/users", { data: { login, password, full_name: login, role: "trainee" } }))).json());
  }
  await ok(await admin.post("/api/v1/admin/users", { data: { login: "e2e-112-outcomes-instructor", password, full_name: "Преподаватель исходов 112", role: "instructor" } }));
  await admin.dispose();

  const instructor = await apiRequest.newContext({ baseURL });
  await ok(await instructor.post("/api/v1/auth/login", { data: { login: "e2e-112-outcomes-instructor", password } }));
  const catalogue = await (await ok(await instructor.get("/api/v1/scenarios?status=archived&exercise_type=operator112_intake&page=1&page_size=20"))).json();
  const scenario = catalogue.items.find((candidate: { source_key?: string }) => candidate.source_key === "pilot-112-medical-01");
  expect(scenario).toBeTruthy();
  const scenarioDetail = await (await ok(await instructor.get(`/api/v1/scenarios/${scenario.id}`))).json();
  const lesson = await (await ok(await instructor.post("/api/v1/lessons", { data: {
    exercise_type: "operator112_intake", title: "Особые исходы 112 E2E", mode: "training", level: "easy",
  } }))).json();
  await ok(await instructor.put(`/api/v1/lessons/${lesson.id}/assignments`, { data: trainees.map((trainee, index) => ({
    workstation_no: 903 + index, user_id: trainee.id, scenario_version_ids: [scenarioDetail.version_id],
  })) }));
  await ok(await instructor.post(`/api/v1/lessons/${lesson.id}/start`, { data: {} }));
  await instructor.dispose();

  for (const [index, testCase] of ([
    { login: "e2e-112-no-contact", action: "Нет контакта", closeReason: "no_contact", message: "Карточка закрыта: нет контакта с заявителем." },
    { login: "e2e-112-call-dropped", action: "Срыв звонка", closeReason: "call_dropped", message: "Карточка закрыта: срыв звонка." },
  ] as const).entries()) {
    await page.goto(`${baseURL}/login`);
    await page.getByLabel("Логин").fill(testCase.login);
    await page.getByLabel("Пароль").fill(password);
    await page.getByLabel("Номер рабочего места (для обучаемого)").fill(String(903 + index));
    await page.getByRole("button", { name: "Войти" }).click();
    await expect(page.getByRole("heading", { name: "Особые исходы 112 E2E" })).toBeVisible();
    const items = await (await ok(await page.request.get("/api/v1/my/items"))).json();
    expect(items).toHaveLength(1);
    await page.getByRole("button", { name: /Открыть карточку №/ }).click();
    await page.getByRole("button", { name: "Открыть вызов" }).click();
    if (testCase.closeReason === "call_dropped") await page.getByRole("button", { name: "Ответить" }).click();
    await page.getByRole("button", { name: testCase.action }).click();
    await expect(page.getByRole("dialog", { name: "Завершение вызова" })).toBeVisible();
    await page.getByRole("dialog").getByRole("button", { name: "Закрыть карточку" }).click();
    await expect(page.getByText(testCase.message)).toBeVisible();
    const closed = await (await ok(await page.request.get(`/api/v1/items/${items[0].id}`))).json();
    expect(closed.close_reason).toBe(testCase.closeReason);
    expect(closed.dispatch).toBeUndefined();
    await page.getByRole("button", { name: "Выйти" }).click();
  }
});
