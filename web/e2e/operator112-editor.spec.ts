import { expect, request as apiRequest, test, type APIRequestContext, type APIResponse } from "@playwright/test";

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080";

async function ok(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

// See operator112.spec.ts's own adminLogin: every e2e spec's setup shares
// the "admin" account, against RFC-001 §9's real 5-attempts/min-per-login
// limiter (never relaxed for tests). Retry past its fixed window instead of
// treating a full-suite run's 429 as spurious flakiness.
async function adminLogin(admin: APIRequestContext) {
  for (let attempt = 0; ; attempt++) {
    const response = await admin.post("/api/v1/auth/login", { data: { login: "admin", password: bootstrapPassword } });
    if (response.status() !== 429 || attempt >= 14) return ok(response);
    await new Promise((resolve) => setTimeout(resolve, 5_000));
  }
}

// 112-7/ADR-027's own e2e (slice-112-7-plan.md's c9): the editor lifecycle
// this repository's other operator112 specs never touch — create through
// the UI, validate, pass the draft yourself in a preview run that
// actually drives the same ARM screen a trainee uses, confirm the
// preview never leaks into the instructor's own lesson list, approve,
// and confirm the now-approved scenario is assignable like any other.
// It deliberately stops short of re-running a full trainee pass of a
// full_case/104-profile item end to end — operator112.spec.ts and
// operator112-profiles.spec.ts already cover that mechanic for other
// scenarios; this spec's own job is the editor and preview machinery
// around it, not full_case intake a second time.
test("112-7 editor: create, validate, pass it yourself, approve, and assign", async ({ page }) => {
  test.setTimeout(180_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  const admin = await apiRequest.newContext({ baseURL });
  await adminLogin(admin);
  await ok(await admin.post("/api/v1/admin/users", {
    data: { login: "e2e-112-editor-instructor", password, full_name: "Автор сценариев 112", role: "instructor" },
  }));
  await admin.dispose();

  const title = `E2E редактор 112 ${Date.now()}`;

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("e2e-112-editor-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();

  await page.getByRole("link", { name: "Готовые сценарии" }).click();
  await page.getByRole("link", { name: "+ Создать сценарий 112" }).click();
  await expect(page.getByRole("heading", { name: "Новый сценарий 112" })).toBeVisible();

  // Tab 1: Общее. The AON starts as the bare "+7" prefix, which the
  // server's structural check reports as an error until completed.
  await page.getByLabel("Название").fill(title);
  await page.getByLabel("АОН (+7 и 10 цифр)").fill("+79161234567");

  // Tab 3: Факты — free_text requires at least one, and the one initial
  // fact this test adds must carry a value (internal/content/validate.go's
  // validateIntake112FreeTextDialogue).
  await page.getByRole("button", { name: "Факты" }).click();
  await page.getByRole("button", { name: "+ Добавить факт" }).click();
  const factRow = page.locator(".scenario-editor-table tbody tr").first();
  await factRow.locator("td").nth(1).locator("input").fill("Жалоба");
  await factRow.locator("td").nth(4).locator("input").fill("рядом сильно пахнет газом");

  // Tab 4: Эталон — expected_types/case_description/expected_services are
  // full_case's own required fields (validateIntake112FullCase).
  await page.getByRole("button", { name: "Эталон" }).click();
  await page.getByRole("button", { name: "Взрыв газа", exact: true }).click();
  await page.getByLabel("Закрытое описание ситуации").fill("Учебный кейс, созданный через веб-редактор 112-7.");
  await page.getByRole("button", { name: "Учебная служба 104 · газ" }).click();

  await page.getByRole("button", { name: "Создать черновик" }).click();
  await expect(page).toHaveURL(/\/instructor\/scenarios\/[0-9a-f-]+\/edit$/);
  await expect(page.getByRole("heading", { name: title })).toBeVisible();
  const scenarioDetailURL = page.url().replace(/\/edit$/, "");

  // An unsaved local edit survives a background refetch of the scenario
  // (window focus → TanStack Query refetchOnWindowFocus) — review
  // 2026-09-26, item 11: the form used to be reset to the saved version.
  await page.getByRole("button", { name: "Общее" }).click();
  const unsavedTitle = `${title} (несохранённая правка)`;
  await page.getByLabel("Название").fill(unsavedTitle);
  const refetch = page.waitForResponse((response) =>
    /\/api\/v1\/scenarios\/[0-9a-f-]+$/.test(new URL(response.url()).pathname) && response.request().method() === "GET");
  await page.evaluate(() => window.dispatchEvent(new Event("visibilitychange")));
  await refetch;
  await expect(page.getByLabel("Название")).toHaveValue(unsavedTitle);
  await expect(page.getByText(/На сервере сохранена другая версия/)).toHaveCount(0);
  await page.getByLabel("Название").fill(title);

  // Проверка: the constructed draft must have no blocking errors — the
  // tab's own label grows a "(N)" suffix (ScenarioEditorRoute's errorCount)
  // the moment any error-severity issue exists, so an exact match on the
  // bare label is the check.
  await page.getByRole("button", { name: "Проверка", exact: true }).click();
  await page.getByRole("button", { name: "Проверить", exact: true }).click();
  await expect(page.getByRole("button", { name: "Проверка", exact: true })).toBeVisible();

  // Пройти самому: a real preview run, driven exactly like a trainee's own
  // full_case item (Operator112ProfileCase reused as-is).
  await page.getByRole("button", { name: "Пройти самому (предпросмотр)" }).click();
  await expect(page).toHaveURL(/\/instructor\/preview\/[0-9a-f-]+\?lesson=[0-9a-f-]+$/);
  await expect(page.getByRole("heading", { name: "Предпросмотр" })).toBeVisible();

  await page.getByRole("dialog", { name: "Входящий звонок" }).getByRole("button", { name: "Принять" }).click();

  // A real exchange with the caller: the reply arrives asynchronously
  // (caller.reply on the worker), so the preview screen must pick it up
  // from its own SSE subscription — without one it stays at "печатает…"
  // until a reload (review 2026-09-26, item 2).
  await expect(page.getByRole("dialog", { name: "Чат с заявителем" })).toBeVisible();
  await page.getByLabel("Сообщение заявителю").fill("Что случилось?");
  await page.getByRole("button", { name: "Отправить" }).click();
  await expect(page.getByText("Заявитель печатает…")).toBeVisible();
  await expect(page.getByText(/Я упал\.\.\. Глаз очень болит/)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("Заявитель печатает…")).toHaveCount(0);

  await page.getByLabel("Тип происшествия").fill("Взрыв газа");
  await page.getByRole("option", { name: "Взрыв газа", exact: true }).click();
  await page.getByRole("button", { name: "Сохранить карточку" }).click();
  await expect(page.getByRole("button", { name: "Добавить службу" })).toBeEnabled();
  await page.getByRole("button", { name: "Добавить службу" }).click();
  await page.getByRole("dialog", { name: "Список оповещаемых служб" }).getByRole("button", { name: "оповестить и сохранить карточку" }).click();
  await expect(page.getByText(/Службы оповещены/)).toBeVisible();
  await page.getByRole("button", { name: "завершить разговор" }).click();
  await page.getByRole("button", { name: "Завершить кейс" }).click();
  // Two "Кейс завершён" notices are visible at once here: the preview
  // screen's own banner (ScenarioPreviewRoute) and Operator112ProfileCase's
  // built-in terminal feedback — match the latter's full text specifically.
  await expect(page.getByText("Кейс завершён. Результат появится после оценки преподавателя.")).toBeVisible();

  // The preview's own auto-assessment appears right here once the worker
  // records it (review 2026-09-26, item 10) — the preview lesson is not in
  // the lesson list, so this screen is the author's only way to it.
  await expect(page.getByRole("heading", { name: "Автоматическая оценка" })).toBeVisible();
  await expect(page.getByText(/^Итог: (готова|требует проверки преподавателем)/)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("link", { name: "Открыть полный разбор карточки" })).toBeVisible();

  // The preview lesson never shows up in the instructor's own list
  // (ADR-027's own exclusion — ListLessonsByInstructor).
  await page.getByRole("button", { name: "Завершить предпросмотр" }).click();
  await expect(page).toHaveURL(`${baseURL}/instructor/scenarios`);
  await page.getByRole("link", { name: "Занятия" }).click();
  await expect(page.getByText(`Предпросмотр: ${title}`)).toHaveCount(0);

  // Утвердить — navigate straight to the scenario's own detail page by
  // the id captured after creation: the catalogue's default filter is
  // status=approved, and this scenario is still a draft until this step.
  await page.goto(scenarioDetailURL);
  await expect(page.getByRole("heading", { name: title })).toBeVisible();
  await page.getByRole("button", { name: "Утвердить" }).click();
  await expect(page.getByText("Сценарий утверждён.")).toBeVisible();
  // Once truly approved, GET /scenarios/{id} finds it through the normal
  // approved-only lookup rather than the owner-only draft fallback, so
  // version_status goes back to absent and the badge falls back to the
  // plain scenario status (scenarioStatusLabel, not versionStatusLabel).
  await expect(page.locator(".status-badge")).toHaveText("утверждён");

  // Assignable like any other approved scenario.
  await page.getByRole("link", { name: "Занятия" }).click();
  await page.getByRole("button", { name: "Создать занятие" }).click();
  await page.getByLabel("Название").fill("Назначение сценария из редактора E2E");
  await page.getByLabel("Упражнение").selectOption("operator112_intake");
  await page.getByRole("button", { name: "Создать", exact: true }).click();
  await page.getByRole("button", { name: "+ Добавить рабочее место" }).click();
  // Scoped to one assignment row: a fresh lesson already shows one row
  // before this click (operator112-profiles.spec.ts's own "click twice,
  // expect 3 rows" confirms the baseline), so an unscoped "Следующий
  // кейс" label would match every row's own <select>, each carrying the
  // same option.
  const select = page.locator(".assignment-row").first().getByLabel("Следующий кейс");
  await expect(select.locator("option", { hasText: title })).toHaveCount(1);
});
