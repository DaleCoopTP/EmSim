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

test("trainee opens an assigned card in the real browser", async ({ page }) => {
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
	const scenarioSummary = scenarios.items.find((candidate: { has_events: boolean }) => !candidate.has_events);
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

	await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("e2e-trainee");
  await page.getByLabel("Пароль").fill(password);
  await page.getByLabel("Номер рабочего места (для обучаемого)").fill(String(workstationNo));
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "E2E phone smoke" })).toBeVisible();
  await page.getByRole("button", { name: "Открыть карточку" }).click();
  await expect(page.getByRole("button", { name: "Вызов" })).toBeVisible();
});
