import { expect, request as apiRequest, test, type APIRequestContext, type APIResponse } from "@playwright/test";

const password = "e2e-password-123";
const bootstrapPassword = "local-only-admin-password";
const workstationNo = 901;

async function expectOK(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response;
}

async function login(request: APIRequestContext, loginName: string, loginPassword = password) {
	await expectOK(await request.post("/api/v1/auth/login", { data: { login: loginName, password: loginPassword } }));
}

test("card → call → recording retry → close works in the real browser", async ({ page }) => {
	const baseURL = process.env.E2E_BASE_URL;
	if (!baseURL) throw new Error("E2E_BASE_URL must be set by e2e/run.mjs");
	const admin = await apiRequest.newContext({ baseURL });
	await login(admin, "admin", bootstrapPassword);
	await expectOK(await admin.put("/api/v1/admin/workstations", {
		data: [{ number: workstationNo, label: "E2E browser workstation" }],
	}));
	const traineeResponse = await expectOK(await admin.post("/api/v1/admin/users", {
		data: { login: "e2e-trainee", password, full_name: "E2E Trainee", role: "trainee", service_code: "dds_district" },
	}));
	const trainee = await traineeResponse.json();
	await expectOK(await admin.post("/api/v1/admin/users", {
		data: { login: "e2e-instructor", password, full_name: "E2E Instructor", role: "instructor" },
	}));

	const instructor = await apiRequest.newContext({ baseURL });
  await login(instructor, "e2e-instructor");
	const scenariosResponse = await expectOK(await instructor.get("/api/v1/scenarios?status=approved&page=1&page_size=20"));
	const scenarios = await scenariosResponse.json();
	const scenarioSummary = scenarios.items.find((candidate: { source_key?: string }) => candidate.source_key === "pilot-phone-01");
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
	await instructor.dispose();
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
  await page.getByLabel("Логин").fill("e2e-trainee");
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
	await page.getByLabel("Кто принял").fill("Руководитель бригады");
	await page.getByLabel("Суть сообщения").fill("Дерево перекрыло дорожку, участок ограждён.");
	await page.getByRole("button", { name: "Завершить" }).click();
	await expect(page.getByRole("button", { name: "Повторить загрузку записи" })).toBeVisible();
	await page.getByRole("button", { name: "Повторить загрузку записи" }).click();
	await expect(page.getByText("Запись готова.")).toBeVisible();
	expect(uploadAttempts).toBe(2);
	expect(callEndCommands).toBe(1);
	await page.getByRole("button", { name: "Принять", exact: true }).click();
	await page.getByRole("button", { name: "Завершить упражнение" }).click();
	await expect(page.getByText("Упражнение завершено.")).toBeVisible();
});
