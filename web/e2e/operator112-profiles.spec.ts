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

test("operator 112: three card-only cases show profiles only after type selection", async ({ page }, testInfo) => {
  test.setTimeout(180_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  const admin = await apiRequest.newContext({ baseURL });
  await adminLogin(admin);
  await ok(await admin.put("/api/v1/admin/workstations", { data: [
    { number: 905, label: "112 профиль 104" }, { number: 906, label: "112 профиль 101" }, { number: 907, label: "112 комбинированный" },
  ] }));
  const trainees = [];
  for (const number of [905, 906, 907]) {
    trainees.push(await (await ok(await admin.post("/api/v1/admin/users", { data: {
      login: `e2e-112-profile-${number}`, password, full_name: `Обучаемый ${number}`, role: "trainee",
    } }))).json());
  }
  await ok(await admin.post("/api/v1/admin/users", { data: {
    login: "e2e-112-profile-instructor", password, full_name: "Преподаватель профилей", role: "instructor",
  } }));
  await admin.dispose();

  const instructorAPI = await apiRequest.newContext({ baseURL });
  await ok(await instructorAPI.post("/api/v1/auth/login", { data: { login: "e2e-112-profile-instructor", password } }));
  const catalogue = await (await ok(await instructorAPI.get("/api/v1/scenarios?status=archived&exercise_type=operator112_intake&page=1&page_size=200"))).json();
  const activeCatalogue = await (await ok(await instructorAPI.get("/api/v1/scenarios?status=approved&exercise_type=operator112_intake&page=1&page_size=200"))).json();
  const cases = [
    { key: "pilot-112-gas-explosion-01", type: "gas_explosion", name: "Взрыв газа", profiles: 1, services: 1 },
    { key: "pilot-112-road-traffic-fire-01", type: "road_traffic_fire", name: "ДТП с пламенем", profiles: 1, services: 2 },
    { key: "pilot-112-gas-road-traffic-fire-01", type: "gas_explosion_road_traffic_fire", name: "Взрыв газа и ДТП с пламенем", profiles: 2, services: 3 },
  ];
  const scenarios = cases.map((entry) => catalogue.items.find((candidate: { source_key?: string }) => candidate.source_key === entry.key));
  for (const scenario of scenarios) expect(scenario).toBeTruthy();
  const versionIds = await Promise.all(scenarios.map(async (scenario) =>
    (await (await ok(await instructorAPI.get(`/api/v1/scenarios/${scenario.id}`))).json()).version_id as string));
  const activeScenarios = ["pilot-112-ai-toyota-fire-01", "pilot-112-ai-car-in-water-01", "pilot-112-ai-mobile-shop-01"]
    .map((key) => activeCatalogue.items.find((candidate: { source_key?: string }) => candidate.source_key === key));
  for (const scenario of activeScenarios) expect(scenario).toBeTruthy();

  await page.goto(`${baseURL}/login`);
  await page.getByLabel("Логин").fill("e2e-112-profile-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("button", { name: "Создать занятие" }).click();
  await page.getByLabel("Название").fill("Профильные карты 112 E2E");
  await page.getByLabel("Упражнение").selectOption("operator112_intake");
  await page.getByRole("button", { name: "Создать", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Профильные карты 112 E2E" })).toBeVisible();
  await page.getByRole("button", { name: "+ Добавить рабочее место" }).click();
  await page.getByRole("button", { name: "+ Добавить рабочее место" }).click();
  const rows = page.locator(".assignment-row");
  await expect(rows).toHaveCount(3);
  for (let index = 0; index < 3; index++) {
    const row = rows.nth(index);
    await row.getByLabel("Рабочее место").selectOption(String(905 + index));
    await row.getByLabel("Обучаемый").selectOption(trainees[index].id);
    await row.locator(".queue-editor select").selectOption(activeScenarios[index].id);
    await row.getByRole("button", { name: "+ В очередь" }).click();
    if (index === 0) {
      await expect(row.locator(".queue-editor select")).not.toHaveValue("");
      await expect(row.getByRole("button", { name: "+ В очередь" })).toBeEnabled();
      await row.locator(".queue-editor select").selectOption(activeScenarios[1].id);
      await row.getByRole("button", { name: "+ В очередь" }).click();
      await expect(row.locator(".queue-editor li")).toHaveCount(2);
    }
  }
  await page.getByRole("button", { name: "Сохранить назначения" }).click();
  await expect(page.getByText("Назначения сохранены.")).toBeVisible();
  await expect(rows.first().getByText(/Сохранено кейсов: 2/)).toBeVisible();
  await rows.first().locator(".queue-editor select").selectOption(activeScenarios[2].id);
  await rows.first().getByRole("button", { name: "+ В очередь" }).click();
  await page.getByRole("button", { name: "Сохранить назначения" }).click();
  await expect(rows.first().getByText(/Сохранено кейсов: 3/)).toBeVisible();
  const savedAssignments = await (await ok(await instructorAPI.get(`/api/v1/lessons/${new URL(page.url()).pathname.split("/").at(-1)}`))).json();
  expect(savedAssignments.assignments[0].scenario_version_ids).toHaveLength(3);
  // The selector exercise above uses active AI cases; replace them with
  // archived card-only fixtures so this test still covers profile behavior.
  const lessonId = new URL(page.url()).pathname.split("/").at(-1)!;
  await ok(await page.request.put(`/api/v1/lessons/${lessonId}/assignments`, { data: trainees.map((trainee, index) => ({
    // Workstation 905 keeps a three-case queue, as before the archiving,
    // so the queue checks after its first case still have a next card.
    workstation_no: 905 + index, user_id: trainee.id, scenario_version_ids: index === 0 ? versionIds : [versionIds[index]],
  })) }));
  await page.reload();
  await page.getByRole("button", { name: /Запустить занятие/ }).click();
  await expect(page.locator(".lesson-heading .status-badge")).toHaveText("Идёт");
  await page.getByRole("button", { name: "Выйти" }).click();

  const itemIDs: string[] = [];
  for (let index = 0; index < 3; index++) {
    await page.getByLabel("Логин").fill(`e2e-112-profile-${905 + index}`);
    await page.getByLabel("Пароль").fill(password);
    await page.getByLabel("Номер рабочего места (для обучаемого)").fill(String(905 + index));
    await page.getByRole("button", { name: "Войти" }).click();
    await expect(page.getByRole("heading", { name: "Профильные карты 112 E2E" })).toBeVisible();
    const items = await (await ok(await page.request.get("/api/v1/my/items"))).json();
    expect(items).toHaveLength(1);
    itemIDs.push(items[0].id);
    const initial = await (await ok(await page.request.get(`/api/v1/items/${items[0].id}`))).json();
    expect(initial.intake_state.mode).toBe("card_only");
    expect(initial.card.profiles ?? {}).toEqual({});
    expect(initial.card.incident_types ?? []).toEqual([]);
    expect(initial.intake_state.catalog.profiles).toEqual([]);
    expect(initial.intake_state.catalog.service_rules).toEqual([]);
    expect(initial.intake_state.catalog.types.every((entry: { profile_ids: string[] }) => entry.profile_ids.length === 0)).toBe(true);
    expect(initial.available_service_codes).toEqual(["pilot_gas_104", "pilot_fire_101", "pilot_ambulance"]);
    expect(initial.intake_reference).toBeUndefined();
    expect(JSON.stringify(initial)).not.toContain("case_description");
    await page.getByRole("button", { name: /Открыть карточку №/ }).click();
    await expect(page.getByText("Учебная карточка без разговора")).toBeVisible();
    await expect(page.getByRole("heading", { name: /Происшествие 10[14]/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Ответить" })).toHaveCount(0);
    await page.getByRole("button", { name: "Открыть кейс" }).click();
    await page.getByLabel("Тип происшествия").fill(cases[index].name);
    await page.getByRole("option", { name: cases[index].name, exact: true }).click();
    await expect(page.locator(".intake-profile-panel")).toHaveCount(cases[index].profiles);
    const active = await (await ok(await page.request.get(`/api/v1/items/${items[0].id}`))).json();
    expect(active.intake_state.catalog.profiles.map((profile: { id: string }) => profile.id)).toEqual(index === 0 ? ["104"] : index === 1 ? ["101"] : ["104", "101"]);
    expect(active.intake_state.catalog.service_rules).toEqual([]);
    expect(active.card.incident_types).toEqual([cases[index].type]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await expect(page.locator(".intake-profile-options button[aria-pressed='true']")).toHaveCount(0);
    if (index === 0 || index === 2) {
      await page.getByRole("button", { name: "Вне помещения (на улице)" }).click();
    }
    if (index > 0) {
      await page.locator(".intake-profile-row").filter({ hasText: "Медицинская помощь" }).getByRole("button", { name: "Да", exact: true }).click();
    }
    await page.getByRole("button", { name: "Сохранить карточку" }).click();
    await expect(page.locator(".arm112-service-tile")).toHaveCount(cases[index].services);
    if (index === 2) await page.screenshot({ path: testInfo.outputPath("operator112-profiles.png"), fullPage: true });
    if (index === 0) {
      page.once("dialog", (dialog) => void dialog.accept());
      await page.getByRole("button", { name: `Убрать тип ${cases[index].name}`, exact: true }).click();
      await expect(page.locator(".intake-profile-panel")).toHaveCount(0);
      await page.getByLabel("Тип происшествия").fill(cases[index].name);
      await page.getByRole("option", { name: cases[index].name, exact: true }).click();
      await expect(page.getByRole("button", { name: "Вне помещения (на улице)" })).toHaveAttribute("aria-pressed", "true");
      await page.getByRole("button", { name: "Сохранить карточку" }).click();
    }
    await page.reload();
    await page.getByRole("button", { name: /Открыть карточку №/ }).click();
    await expect(page.locator(".intake-profile-panel")).toHaveCount(cases[index].profiles);
    await page.getByRole("button", { name: "Добавить службу" }).click();
    await page.getByRole("dialog", { name: "Список оповещаемых служб" }).getByRole("button", { name: "оповестить и сохранить карточку" }).click();
    await expect(page.getByText(/Службы оповещены/)).toBeVisible();
    await page.getByRole("button", { name: "Завершить кейс" }).click();
    await expect(page.getByText(/Кейс завершён/)).toBeVisible();
    await page.getByRole("button", { name: "К списку вызовов" }).click();
    await expect(page.getByRole("heading", { name: "Список входящих кейсов" })).toBeVisible();
    await expect(page.getByRole("button", { name: /Открыть карточку №/ })).toHaveCount(index === 0 ? 2 : 1);
    if (index === 0) {
      const queued = await (await ok(await page.request.get("/api/v1/my/items"))).json();
      expect(queued).toHaveLength(2);
      expect(queued[1].state).toBe("offered");
      await page.getByRole("button", { name: `Открыть карточку № ${queued[1].card_number}` }).click();
      await expect(page.getByRole("button", { name: "Открыть кейс" })).toBeVisible();
    }
    await page.getByRole("button", { name: "Выйти" }).click();
  }

  await page.getByLabel("Логин").fill("e2e-112-profile-instructor");
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Занятия" })).toBeVisible();
  const reviewed = await (await ok(await page.request.get(`/api/v1/items/${itemIDs[2]}`))).json();
  expect(reviewed.intake_reference.expected_types).toEqual(["gas_explosion_road_traffic_fire"]);
  expect(reviewed.card.profiles["101"]).toBeTruthy();
  expect(reviewed.card.profiles["104"]).toBeTruthy();
  expect(reviewed.dispatch).toBeUndefined();
  expect(reviewed.notification.services.map((entry: { service_code: string }) => entry.service_code).sort()).toEqual(["pilot_ambulance", "pilot_fire_101", "pilot_gas_104"]);
  await page.goto(`${baseURL}/instructor/items/${itemIDs[2]}/review`);
  await expect(page.getByRole("heading", { name: "Кейс без разговора" })).toBeVisible();
  await expect(page.getByText(/Добавил тип: Взрыв газа и ДТП с пламенем/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Активные профильные карты" })).toBeVisible();
  await expect(page.getByText(/pilot_gas_104: Добавлена карта 104/)).toBeVisible();
  await expect(page.getByText(/pilot_fire_101: Добавлена карта 101/)).toBeVisible();
  const notifiedLine = page.locator("p", { hasText: "Оповещены:" });
  await expect(notifiedLine).toBeVisible();
  await expect(notifiedLine).toContainText("pilot_gas_104");
  await expect(notifiedLine).toContainText("pilot_fire_101");
  await expect(notifiedLine).toContainText("pilot_ambulance");
  await instructorAPI.dispose();
});
