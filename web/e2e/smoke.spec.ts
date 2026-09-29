import { expect, request as apiRequest, test, type APIRequestContext, type APIResponse } from "@playwright/test";

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const workstationNo = 901;

async function expectOK(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

// Every e2e spec's own setup logs into the same "admin" account, against
// RFC-001 §9's real 5-attempts/min-per-login limiter (never relaxed for
// tests, internal/auth/ratelimit.go). A fast full-suite run can stack more
// admin logins than that into one minute; retry past the limiter's fixed
// window instead of treating its 429 as spurious flakiness.
async function login(request: APIRequestContext, loginName: string, loginPassword = password) {
	for (let attempt = 0; ; attempt++) {
		const response = await request.post("/api/v1/auth/login", { data: { login: loginName, password: loginPassword } });
		if (response.status() !== 429 || attempt >= 14) { await expectOK(response); return; }
		await new Promise((resolve) => setTimeout(resolve, 5_000));
	}
}

test("ARM-112 acceptance: login → queue → card → monitor → call → crew reports → incoming call → status pencil → close", async ({ page }) => {
	// ДДС-2 (ADR-031): crew reports arrive in real scenario time after the
	// call to the crew leader — the notice after 15 s, the incoming call
	// after 35 s.
	test.setTimeout(150_000);
	const baseURL = process.env.E2E_BASE_URL;
	if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
	const admin = await apiRequest.newContext({ baseURL });
	await login(admin, "admin", bootstrapPassword);
	await expectOK(await admin.put("/api/v1/admin/workstations", {
		data: [{ number: workstationNo, label: "E2E browser workstation" }],
	}));
	const traineeResponse = await expectOK(await admin.post("/api/v1/admin/users", {
		data: { login: "e2e-trainee", password, full_name: "E2E Trainee", role: "trainee", service_code: "dds_district_chertanovo" },
	}));
	const trainee = await traineeResponse.json();
	await expectOK(await admin.post("/api/v1/admin/users", {
		data: { login: "e2e-instructor", password, full_name: "E2E Instructor", role: "instructor" },
	}));

	const instructor = await apiRequest.newContext({ baseURL });
  await login(instructor, "e2e-instructor");
	const scenariosResponse = await expectOK(await instructor.get("/api/v1/scenarios?status=approved&page=1&page_size=20"));
	const scenarios = await scenariosResponse.json();
	const scenarioSummary = scenarios.items.find((candidate: { source_key?: string }) => candidate.source_key === "dds-district-tree-cycle-01");
	expect(scenarioSummary).toBeTruthy();
	const scenarioResponse = await expectOK(await instructor.get(`/api/v1/scenarios/${scenarioSummary.id}`));
  const scenario = await scenarioResponse.json();
  const lessonResponse = await expectOK(await instructor.post("/api/v1/lessons", {
    data: { exercise_type: "dds_processing", title: "E2E phone smoke", mode: "training", level: "easy" },
  }));
  const lesson = await lessonResponse.json();
  await expectOK(await instructor.put(`/api/v1/lessons/${lesson.id}/assignments`, {
    data: [{ workstation_no: workstationNo, user_id: trainee.id, scenario_version_ids: [scenario.version_id] }],
  }));
	await expectOK(await instructor.post(`/api/v1/lessons/${lesson.id}/start`));
	await admin.dispose();

	await page.addInitScript(() => {
		const descriptor = Object.getOwnPropertyDescriptor(MediaStreamTrack.prototype, "enabled");
		if (!descriptor?.set || !descriptor.get) return;
		Object.defineProperty(MediaStreamTrack.prototype, "enabled", {
			configurable: true,
			get: descriptor.get,
			set(value: boolean) {
				(window as unknown as { __e2eTrackEnabled?: boolean[] }).__e2eTrackEnabled ??= [];
				(window as unknown as { __e2eTrackEnabled: boolean[] }).__e2eTrackEnabled.push(value);
				descriptor.set?.call(this, value);
			},
		});
	});
	let uploadAttempts = 0;
	let callEndCommands = 0;
	page.on("request", (request) => {
		if (!request.url().endsWith("/actions") || request.method() !== "POST") return;
		if (JSON.parse(request.postData() ?? "{}").type === "call_end") callEndCommands += 1;
	});
	await page.route("**/api/v1/items/*/calls/*/recording", async (route) => {
		uploadAttempts += 1;
		if (uploadAttempts === 1) await route.abort("failed");
		else await route.continue();
	});

	await page.goto(`${baseURL}/login`);
	await page.getByLabel("Логин").fill("wrong-login");
	await page.getByLabel("Пароль").fill("wrong-password");
	await page.getByRole("button", { name: "Войти" }).click();
	await expect(page.getByRole("alert")).toBeVisible();
	await page.getByLabel("Логин").fill("e2e-instructor");
	await page.getByLabel("Пароль").fill(password);
	await page.getByRole("button", { name: "Войти" }).click();
	await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();
	await page.goto(`${baseURL}/instructor/lessons/${lesson.id}/monitor`);
	await expect(page.getByRole("heading", { name: `Монитор: ${lesson.title}` })).toBeVisible();
	await page.getByRole("button", { name: "Выйти" }).click();
	await expect(page.getByRole("heading", { name: "ВХОД В СИСТЕМУ" })).toBeVisible();

	await page.getByLabel("Логин").fill("e2e-trainee");
	await page.getByLabel("Логин").press("Tab");
	await expect(page.getByLabel("Пароль")).toBeFocused();
	await page.getByLabel("Пароль").fill(password);
	await page.getByLabel("Номер рабочего места (для обучаемого)").fill(String(workstationNo));
	await page.getByRole("button", { name: "Войти" }).click();
	await expect(page.getByRole("heading", { name: "E2E phone smoke" })).toBeVisible();
	await page.getByRole("button", { name: /Открыть карточку №/ }).click();

	await page.getByRole("button", { name: "Открыть карточку" }).click();
	await page.getByRole("button", { name: "Вызов" }).click();
	await expect(page.getByText("Идёт запись доклада.")).toBeVisible();
	await page.getByRole("button", { name: "Mute" }).click();
	await expect(page.getByRole("button", { name: "Включить микрофон" })).toHaveAttribute("aria-pressed", "true");
	expect(await page.evaluate(() => (window as unknown as { __e2eTrackEnabled?: boolean[] }).__e2eTrackEnabled ?? [])).toContain(false);
	await page.getByRole("button", { name: "Включить микрофон" }).click();
	expect(await page.evaluate(() => (window as unknown as { __e2eTrackEnabled?: boolean[] }).__e2eTrackEnabled ?? [])).toContain(true);
	// ДДС-3 (ADR-032): for a service with terminal statuses an outgoing
	// call's written report is optional, so ending is allowed right away.
	await expect(page.getByLabel("Кто принял (необязательно)")).toBeVisible();
	await expect(page.getByRole("button", { name: "Завершить", exact: true })).toBeEnabled();
	await page.getByLabel("Кто принял").fill("Руководитель бригады");
	await page.getByLabel("Суть сообщения").fill("Дерево перекрыло дорожку, участок ограждён.");
	await page.getByRole("button", { name: "Завершить" }).click();
	await expect(page.getByRole("button", { name: "Повторить загрузку записи" })).toBeVisible();
	await page.getByRole("button", { name: "Повторить загрузку записи" }).click();
	await expect(page.getByText("Запись готова.")).toBeVisible();
	expect(uploadAttempts).toBe(2);
	expect(callEndCommands).toBe(1);
	// ADR-030: the reaction cycle is worked through the service's own
	// status pencil; the terminal status closes the card by itself.
	await expect(page.getByRole("button", { name: "Завершить упражнение" })).toHaveCount(0);
	await expect(page.getByRole("button", { name: "Добавить комментарий" })).toHaveCount(0);
	// ADR-031: the phone groups contacts by role; crew reports are anchored
	// on the call that just ended, not on the trainee's statuses.
	const comms = page.getByRole("complementary", { name: "Связь с бригадой" });
	await expect(comms.getByRole("group", { name: "Отдел контроля 112" })).toBeVisible();
	await expect(comms.getByRole("group", { name: "Заявитель" })).toBeVisible();
	const saveStatus = async (label: string, comment: string) => {
		await page.getByRole("button", { name: "Проставить статус реагирования" }).click();
		await page.getByLabel("Статус реагирования").selectOption({ label });
		await page.getByLabel(/^Комментарий/).fill(comment);
		await page.getByRole("button", { name: "Сохранить", exact: true }).click();
		await expect(page.locator(".dds-service-block-head > span")).toContainText(label);
	};
	await saveStatus("Принята", "Принята, бригада направлена.");
	await expect(comms.getByText("Бригада выехала, будем на месте через 15 минут.")).toBeVisible({ timeout: 30_000 });
	// The instructor's monitor sees the delivered report without a reaction yet.
	const monitorResponse = await expectOK(await instructor.get(`/api/v1/lessons/${lesson.id}/monitor`));
	const monitor = await monitorResponse.json();
	expect(monitor.rows[0].reports).toEqual([expect.objectContaining({ event_key: "e1", from_label: "Руководитель аварийной бригады", reaction_at: null, missed: false })]);
	await saveStatus("Начало реагирования", "Бригада выехала, прибытие через 15 минут.");
	// The incoming call rings: who calls is shown, their words only after answering.
	const incoming = comms.getByRole("alert");
	await expect(incoming).toContainText("Руководитель аварийной бригады", { timeout: 40_000 });
	await expect(comms.getByText("Мы на месте", { exact: false })).toHaveCount(0);
	await incoming.getByRole("button", { name: "Ответить" }).click();
	await expect(comms.getByText("«Диспетчер, это бригада. Мы на месте, дерево лежит поперёк проезда.»")).toBeVisible();
	await expect(page.getByRole("button", { name: "Вызов" })).toBeDisabled();
	await comms.getByRole("button", { name: "Завершить разговор" }).click();
	await expect(comms.locator(".dds-comms-meta").filter({ hasText: /Входящий звонок.*принят/ })).toBeVisible();
	await saveStatus("Прибытие", "Бригада на месте.");
	await saveStatus("Проведение работ", "Распил дерева, вызвана автовышка.");
	await page.getByRole("button", { name: "Проставить статус реагирования" }).click();
	await page.getByLabel("Статус реагирования").selectOption({ label: "Работы завершены" });
	await expect(page.getByText("Сохранение этого статуса закроет карточку для редактирования.", { exact: false })).toBeVisible();
	await page.getByLabel(/^Комментарий/).fill("Дерево убрано, проезд свободен.");
	await page.getByRole("button", { name: "Сохранить", exact: true }).click();
	await expect(page.getByText("Упражнение завершено.")).toBeVisible();
	await expect(page.getByRole("button", { name: "Проставить статус реагирования" })).toHaveCount(0);
	await page.getByRole("button", { name: "▾ История статусов" }).click();
	await expect(page.locator(".dds-service-history li")).toHaveCount(5);

	// The instructor's review shows the same saved statuses with comments.
	const queueResponse = await expectOK(await instructor.get(`/api/v1/lessons/${lesson.id}/assessments`));
	const [row] = await queueResponse.json();
	await instructor.dispose();
	await page.getByRole("button", { name: "Выйти" }).click();
	await page.getByLabel("Логин").fill("e2e-instructor");
	await page.getByLabel("Пароль").fill(password);
	await page.getByRole("button", { name: "Войти" }).click();
	await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();
	await page.goto(`${baseURL}/instructor/items/${row.item_id}/review`);
	// The DDS review is a set of collapsible sections; the saved statuses
	// live under «Карточка и эталон».
	await page.locator("summary").filter({ hasText: "Карточка и эталон" }).click();
	await expect(page.getByRole("heading", { name: "Статусы, проставленные обучаемым" })).toBeVisible();
	await expect(page.getByRole("listitem").filter({ hasText: "Проведение работ — Распил дерева, вызвана автовышка." })).toBeVisible();
	await expect(page.getByText("Статус карточки: Завершена")).toBeVisible();
});
