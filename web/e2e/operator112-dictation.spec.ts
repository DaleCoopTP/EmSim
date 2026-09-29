import { expect, request as apiRequest, test, type APIResponse } from "@playwright/test";

// 112-8a/ADR-037: dictation in the caller chat. The api runs with
// DICTATION=stub (web/e2e/run.mjs), the browser has a fake microphone
// (playwright.config.ts): the phrase is recorded as a real 16 kHz WAV,
// validated by the server and answered with the stub's fixed text.

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080";
const stubPhrase = "Горит дом, улица Ленина, дом пять.";

async function ok(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

async function adminLogin(admin: Awaited<ReturnType<typeof apiRequest.newContext>>) {
  for (let attempt = 0; ; attempt++) {
    const response = await admin.post("/api/v1/auth/login", { data: { login: "admin", password: bootstrapPassword } });
    if (response.status() !== 429 || attempt >= 14) return ok(response);
    await new Promise((resolve) => setTimeout(resolve, 5_000));
  }
}

test("operator 112: dictation fills the chat input, the sent line carries the voice mark", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  const admin = await apiRequest.newContext({ baseURL });
  await adminLogin(admin);
  await ok(await admin.put("/api/v1/admin/workstations", { data: [{ number: 910, label: "112 dictation" }] }));
  const trainee = await (await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-112-dict-trainee", password, full_name: "Обучаемый диктовки 112", role: "trainee",
  } }))).json();
  await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-112-dict-instructor", password, full_name: "Преподаватель диктовки 112", role: "instructor",
  } }));
  await admin.dispose();

  const instructorAPI = await apiRequest.newContext({ baseURL });
  await ok(await instructorAPI.post("/api/v1/auth/login", { data: { login: "e2e-112-dict-instructor", password } }));
  const catalogue = await (await ok(await instructorAPI.get("/api/v1/scenarios?status=archived&exercise_type=operator112_intake&page=1&page_size=200"))).json();
  const scenario = catalogue.items.find((candidate: { source_key?: string }) => candidate.source_key === "pilot-112-free-text-chat-01");
  expect(scenario).toBeTruthy();
  const scenarioDetail = await (await ok(await instructorAPI.get(`/api/v1/scenarios/${scenario.id}`))).json();
  await instructorAPI.dispose();

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("e2e-112-dict-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();
  await page.getByRole("button", { name: "Создать занятие" }).click();
  await page.getByLabel("Название").fill("Диктовка 112 E2E");
  await page.getByLabel("Упражнение").selectOption("operator112_intake");
  await page.getByRole("button", { name: "Создать", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Диктовка 112 E2E" })).toBeVisible();
  const lessonId = new URL(page.url()).pathname.split("/").at(-1)!;
  await ok(await page.request.put(`/api/v1/lessons/${lessonId}/assignments`, { data: [{
    workstation_no: 910, user_id: trainee.id, scenario_version_ids: [scenarioDetail.version_id],
  }] }));
  await page.reload();
  await page.getByRole("button", { name: /Запустить занятие/ }).click();
  await expect(page.locator(".lesson-heading .status-badge")).toHaveText("Идёт");
  await page.getByRole("button", { name: "Выйти" }).click();

  await page.getByLabel("Логин").fill("e2e-112-dict-trainee");
  await page.getByLabel("Пароль").fill(password);
  await page.getByLabel("Номер рабочего места (для обучаемого)").fill("910");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Диктовка 112 E2E" })).toBeVisible();
  const items = await (await ok(await page.request.get("/api/v1/my/items"))).json();
  const itemID = items[0].id;

  // The hint exists only once the engine is on, and only for a free_text chat.
  const initial = await (await ok(await page.request.get(`/api/v1/items/${itemID}`))).json();
  expect(initial.dictation).toEqual({ available: true, max_seconds: 30 });

  await page.getByRole("button", { name: /Открыть карточку №/ }).click();
  await page.getByRole("button", { name: "Открыть кейс" }).click();
  await page.getByRole("button", { name: "ответить", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Чат с заявителем" })).toBeVisible();

  const chatInput = page.getByLabel("Сообщение заявителю");
  const mic = page.getByRole("button", { name: "Надиктовать сообщение" });
  await expect(mic).toBeEnabled();

  // Record → stop → the recognised phrase lands in the input, unsent.
  await mic.click();
  await expect(page.getByRole("button", { name: "Остановить запись" })).toBeVisible();
  await expect(page.getByText("Идёт запись…")).toBeVisible();
  await page.waitForTimeout(1500);
  await page.getByRole("button", { name: "Остановить запись" }).click();
  await expect(chatInput).toHaveValue(stubPhrase, { timeout: 10_000 });
  const untouched = await (await ok(await page.request.get(`/api/v1/items/${itemID}`))).json();
  expect(untouched.intake_state.transcript).toHaveLength(0);

  // The operator edits the text, and the sent line carries the mark.
  await chatInput.fill(`${stubPhrase} Есть пострадавшие?`);
  await page.getByRole("button", { name: "Отправить" }).click();
  await expect(chatInput).toHaveValue("");
  await expect(page.getByText("Заявитель печатает…")).toBeVisible();
  const afterVoice = await (await ok(await page.request.get(`/api/v1/items/${itemID}`))).json();
  expect(afterVoice.intake_state.transcript[0]).toMatchObject({ speaker: "operator", text: `${stubPhrase} Есть пострадавшие?`, input: "voice" });

  // Wait for the caller's reply, then type a message: no mark.
  await expect(page.getByText(/Я упал\.\.\. Глаз очень болит/)).toBeVisible({ timeout: 10_000 });
  await chatInput.fill("Какой у вас адрес?");
  await page.getByRole("button", { name: "Отправить" }).click();
  await expect(chatInput).toHaveValue("");
  const afterTyped = await (await ok(await page.request.get(`/api/v1/items/${itemID}`))).json();
  const operatorLines = afterTyped.intake_state.transcript.filter((line: { speaker: string }) => line.speaker === "operator");
  expect(operatorLines).toHaveLength(2);
  expect(operatorLines[1].input).toBeUndefined();
});
