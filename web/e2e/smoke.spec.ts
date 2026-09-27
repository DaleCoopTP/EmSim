import { expect, request as apiRequest, test, type APIRequestContext, type APIResponse, type Locator, type Page } from "@playwright/test";

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const workstationNo = 901;
const desktopViewports = [
	{ width: 1366, height: 768 },
	{ width: 1920, height: 1080 },
] as const;

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

async function expectDesktopScreenshots(page: Page, name: string, masks: Locator[] = []) {
	for (const viewport of desktopViewports) {
		await page.setViewportSize(viewport);
		await expect(page).toHaveScreenshot(`${name}-${viewport.width}.png`, {
			animations: "disabled",
			mask: masks,
			maxDiffPixelRatio: 0.002,
		});
	}
}

test("ARM-112 acceptance: login → queue → card → monitor → call → status pencil → close", async ({ page }) => {
	test.setTimeout(90_000);
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
	await expectDesktopScreenshots(page, "login");
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
	await expectDesktopScreenshots(page, "live-monitor", [
		page.locator(".layout-clock"),
		page.locator(".monitor-heading p"),
	]);
	await page.getByRole("button", { name: "Выйти" }).click();
	await expect(page.getByRole("heading", { name: "ВХОД В СИСТЕМУ" })).toBeVisible();

	await page.getByLabel("Логин").fill("e2e-trainee");
	await page.getByLabel("Логин").press("Tab");
	await expect(page.getByLabel("Пароль")).toBeFocused();
	await page.getByLabel("Пароль").fill(password);
	await page.getByLabel("Номер рабочего места (для обучаемого)").fill(String(workstationNo));
	await page.getByRole("button", { name: "Войти" }).click();
	await expect(page.getByRole("heading", { name: "E2E phone smoke" })).toBeVisible();
	await expectDesktopScreenshots(page, "incident-queue", [
		page.locator(".layout-clock"),
		page.locator(".incident-queue tbody td:nth-child(2)"),
	]);
	await page.getByRole("button", { name: /Открыть карточку №/ }).click();
	await expectDesktopScreenshots(page, "dds-card", [
		page.locator(".layout-clock"),
		page.locator(".dds-card-registration"),
		page.locator(".dds-item-status"),
	]);

	await page.getByRole("button", { name: "Открыть карточку" }).click();
	await page.getByRole("button", { name: "Вызов" }).click();
	await expect(page.getByText("Идёт запись доклада.")).toBeVisible();
	await page.getByRole("button", { name: "Mute" }).click();
	await expect(page.getByRole("button", { name: "Включить микрофон" })).toHaveAttribute("aria-pressed", "true");
	expect(await page.evaluate(() => (window as unknown as { __e2eTrackEnabled?: boolean[] }).__e2eTrackEnabled ?? [])).toContain(false);
	await page.getByRole("button", { name: "Включить микрофон" }).click();
	expect(await page.evaluate(() => (window as unknown as { __e2eTrackEnabled?: boolean[] }).__e2eTrackEnabled ?? [])).toContain(true);
	await expect(page.getByRole("button", { name: "Завершить", exact: true })).toBeDisabled();
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
	const saveStatus = async (label: string, comment: string) => {
		await page.getByRole("button", { name: "Проставить статус реагирования" }).click();
		await page.getByLabel("Статус реагирования").selectOption({ label });
		await page.getByLabel(/^Комментарий/).fill(comment);
		await page.getByRole("button", { name: "Сохранить", exact: true }).click();
		await expect(page.locator(".dds-service-block-head > span")).toContainText(label);
	};
	await saveStatus("Принята", "Принята, бригада направлена.");
	await saveStatus("Начало реагирования", "Бригада выехала, прибытие через 15 минут.");
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
	await expect(page.getByRole("heading", { name: "Статусы реагирования" })).toBeVisible();
	await expect(page.getByRole("listitem").filter({ hasText: "Проведение работ — Распил дерева, вызвана автовышка." })).toBeVisible();
	await expect(page.getByText("Статус карточки: Завершена")).toBeVisible();
});
