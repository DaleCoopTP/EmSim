# План среза ДДС-7: обратная связь, графики, рекомендация уровня

Дата: 2026-09-29. Статус: план; решения §2 предложены исполнителем и ждут подтверждения пользователя, после него — ADR-036 (c1).

## 1. Контекст и границы

`slice-planning-dds.md` §ДДС-7 (бывший срез 10 `slice-planning.md`) включает:
- комментарий преподавателя обучаемому, графики и тепловую карту нарушений;
- детерминированную рекомендацию уровня с принятием преподавателем;
- LLM-совет обучаемому и «типичные ошибки группы».

**Решение пользователя 29.09:** LLM-часть (совет обучаемому «над чем работать», «типичные ошибки группы» от модели) **исключена из среза** и уходит в техдолг; в MVP её нет. Срез целиком детерминированный, без обращений к модели; ADR-003 не затрагивается. Таблица `advice`, задача совета и `advice_due_at` не используются. Всё относится только к `dds_processing`; рекомендации и графики 112 — техдолг (`slice-planning-112.md` §14).

Что есть сейчас (проверено по коду):
- **ЛК обучаемого** (`web/src/routes/trainee/History.tsx`): `GET /my/progress` (уровень, средний балл, баллы по занятиям таблицей, `error_frequency` списком id критериев) и `GET /my/results` (карточки, безопасные ошибки с `guide_ref`). Графиков нет, названия критериев в «частых ошибках» не показаны — только id.
- **Преподаватель** видит обучаемого только внутри занятия: отчёт, оценки, разбор карточки. Страницы обучаемого и его прогресса нет. `GET /users/{id}/progress`, `GET/POST /users/{id}/recommendation` и `GET /groups/progress` объявлены в `openapi.yaml`, но не реализованы.
- **Комментария обучаемому нет.** У экспертной ревизии есть `reason` («почему изменил»), но это обоснование правки балла, а не обратная связь, и ревизия требует пересчёта критериев.
- **Основание рекомендации.** `trainee_assessment_state (user_id, exercise_type, version)` существует (миграция 00009), но `version` растёт только при экспертной ревизии (`internal/assessment/service.go:603`); запись автооценки его не меняет, хотя RFC §6/§7.4 требует роста при любой новой итоговой оценке. Таблицы `recommendations` в миграциях нет (есть только в `schema.sql`); `advice` в этом срезе не создаётся.
- **Уровень.** `users.level` — поле профиля, `lessons.level` — уровень занятия, `runs.level_at_start = lesson.Level`. Смена `users.level` сейчас ни на что не влияет, кроме отображения; менять её может только администратор.
- **Библиотеки графиков в `web/` нет** (зависимости — React, TanStack Query, react-router).
- **Отчёт занятия** не несёт статусов критериев по карточке (`reporting.ReportItem`): тепловую карту по нему не построить.

## 2. Решения (предложение — подтвердить)

1. **Комментарий преподавателя** — отдельная сущность, не ревизия оценки. Одна запись на карточку (`item_feedback`, владелец — `assessment`): текст до 2000 символов, автор, время; преподаватель может править и удалять, история правок в `audit_log`. Обучаемый видит текст в `/my/results`; балл и ревизии он не меняет. Доступен после закрытия карточки, для `training`-занятий (не `intro`/`preview`).
2. **Политика рекомендации `dds/level-v1`** (детерминированная, чистая функция):
   - **Основание** — последние **N = 5** итоговых оценок обучаемого по ДДС со статусом `ready`, из занятий `mode=training`, без `interruptions`, с `level_at_start = users.level` (только результаты на текущем уровне).
   - Меньше 5 таких оценок → рекомендации нет («недостаточно истории, k из 5»).
   - **Повысить** (easy→medium→hard): средний балл ≥ **85**, зачтено ≥ **4 из 5**, и ни одного срабатывания критического критерия в основании.
   - **Понизить** (hard→medium→easy): средний балл < **60** или не зачтено ≥ **3 из 5**.
   - Иначе — **оставить**. На hard «повысить» и на easy «понизить» превращаются в «оставить».
   - Основание сохраняет `assessment_id`/`revision`, балл, `passed`, критические ошибки и `level_at_start` каждой оценки.
3. **Когда считается.** Рекомендация вычисляется при чтении (≤ 5 строк, без очереди задач). Её `id` детерминирован: UUIDv5 от `(user_id, exercise_type, basis_version, policy_version)`. В таблицу `recommendations` строка пишется только при решении преподавателя — «применить» или «отклонить». Отклонённая или применённая рекомендация для той же `basis_version` больше не показывается; новая итоговая оценка (авто или экспертная) увеличивает `basis_version` и даёт новую.
4. **Применение** (`POST /users/{id}/recommendation`, контракт уже есть): одна транзакция `users` → `trainee_assessment_state` (порядок блокировок RFC §8). Под блокировкой рекомендация пересчитывается; если `id`/`basis_version` не совпали — `409 stale_recommendation`. Иначе `users.level` меняется через порт `auth`, пишется строка `recommendations` (`decision=applied`), `version` основания +1, audit. Отклонение — `POST /users/{id}/recommendation/decline` с тем же телом, без смены уровня. «Оставить» ничего не меняет и кнопок не имеет.
5. **Кто видит.** Любой преподаватель видит прогресс и рекомендацию любого обучаемого ДДС и применяет её: уровень — общий атрибут профиля, а обучаемые переходят между преподавателями. Обучаемый видит свою актуальную рекомендацию в ЛК только для чтения, без оснований с чужими данными.
6. **Графики** — встроенный SVG без новых зависимостей, тёмная/светлая тема через существующие CSS-переменные:
   - **ЛК обучаемого и страница обучаемого у преподавателя:** линия баллов по карточкам во времени с линией порога; столбцы «доля выполнения по критериям» (met = 1, partial = 0,5) с названиями критериев из `rubric_effective`.
   - **Отчёт занятия:** тепловая карта «обучаемые × критерии» (доля выполнения за занятие; `not_applicable` — пусто, `needs_review` не входит). Критерии объединяются по id; для занятия на v1 показываются свои.
7. **Навигация преподавателя.** Новый раздел «Обучаемые» (`/instructor/trainees`): список обучаемых ДДС с уровнем, числом оценок, средним баллом и отметкой «есть рекомендация». Страница обучаемого (`/instructor/trainees/:userId`) — графики, последние карточки со ссылкой на разбор, рекомендация с «Применить»/«Отклонить». Имя в отчёте занятия ведёт на эту страницу. В редакторе назначений рядом с обучаемым показывается его текущий уровень и предупреждение, если он не совпадает с уровнем занятия.
8. **Исправление основания.** Запись итоговой автооценки (`recordAutoTx`) и финализатор с `needs_review` увеличивают `trainee_assessment_state.version` (кроме `preview`), как уже делает экспертная ревизия.

## 3. Коммиты

**c1 — `docs: DDS-7 plan and ADR-036 (feedback, charts, level recommendation)`.**
- `design-docs/adr/036-dds-feedback-and-level-recommendation.md`: решения §2. Отвергнуто: комментарий как экспертная ревизия; рекомендация задачей `recommendation.compute` в очереди (лишняя асинхронность для ≤ 5 строк); библиотека графиков. Последствия: LLM-совет и «типичные ошибки группы» — техдолг, таблица `advice` не создаётся.
- `adr/README.md`, `slice-planning-dds.md` (ДДС-7 → «в работе», LLM-часть — техдолг), `LOG.MD`.
- Проверка: `python3 design-docs/contracts/check.py`.

**c2 — `contracts: item feedback, trainee progress, level recommendation`.**
- `openapi.yaml`:
  - `PUT/DELETE /items/{id}/feedback` (`{text}`), `ItemResult.instructor_comment: {text, author_name, updated_at} | null`;
  - `GET /trainees?exercise_type` → `[{user, level, ready_items, avg_score, has_recommendation}]`;
  - реализуемая форма `GET /users/{id}/progress` (ответ `Progress` + `score_series[]` и `criteria_stats[{id, title, met_rate, n}]`), та же добавка в `GET /my/progress`;
  - `Recommendation` дополнить `decision`, `basis` (оценки основания), `reason` («insufficient_history», «avg_high»…); `GET /my/recommendation`; `POST /users/{id}/recommendation/decline`;
  - `ReportItem.criteria: [{id, status}]` для тепловой карты.
- `schema.sql`: `item_feedback`; `recommendations` — колонка `decision ('applied'|'declined')`, `decided_by/decided_at` вместо `applied_*`.
- Проверка: `check.py`, `make verify-web` (генерация типов).

**c3 — `assessment: bump the recommendation basis on every final assessment`.**
- `version` основания растёт при записи автооценки и при финализации `needs_review` (не для `preview`). Тест: авто → +1, экспертная → +1, preview → без изменений.

**c4 — `assessment: instructor feedback on an item`.**
- Миграция `00022_item_feedback.sql`, `ExpectedSchemaVersion` 22.
- `assessment.Service.SetFeedback/DeleteFeedback`: преподаватель, карточка закрыта, занятие `training`; audit. HTTP-обработчики.
- `reporting`: `instructor_comment` в `/my/results` и в отчёте.

**c5 — `assessment: dds/level-v1 recommendation policy`.**
- `internal/assessment/recommend.go`: чистая функция `Recommend(history, level) → Recommendation` и табличные тесты на все ветки (мало истории, повышение, понижение по среднему и по числу незачтённых, критическая ошибка блокирует повышение, границы easy/hard, прерванные и не-`ready` исключены).

**c6 — `assessment, auth: read, apply and decline a level recommendation`.**
- Миграция `00023_recommendations.sql`, `ExpectedSchemaVersion` 23.
- Порт `auth` для смены `users.level` в чужой транзакции (владелец таблицы — `auth`); application use case в `cmd/emsim`/`assessment` по RFC §7.4.
- `GET /users/{id}/recommendation`, `GET /my/recommendation`, `POST …/recommendation`, `POST …/recommendation/decline`; `409 stale_recommendation`. Тесты обработчиков и ролей.

**c7 — `reporting: trainee list, progress series, criterion stats, lesson heatmap data`.**
- `GET /trainees`, `GET /users/{id}/progress`; `score_series`/`criteria_stats` в прогрессе; `criteria` в `ReportItem`. Только `ready`-итоговые, только `training`, без смешения упражнений.

**c8 — `web: feedback, charts, trainee page, recommendation`.**
- `components/charts/ScoreLine.tsx`, `CriterionBars.tsx`, `Heatmap.tsx` (SVG).
- ЛК: графики, названия критериев в частых ошибках, комментарий преподавателя у карточки, актуальная рекомендация (только чтение).
- `ItemReview.tsx`: поле комментария обучаемому.
- `routes/instructor/Trainees.tsx`, `TraineeDetail.tsx`; пункт меню; ссылка из отчёта; тепловая карта в `LessonReport.tsx`; уровень обучаемого в редакторе назначений.

**c9 — `test: DDS-7 through api and worker`.**
- `test/integration/dds_recommendation_test.go`: пять оценок на easy через настоящие api и worker → «повысить» → применить → уровень medium, `version` +1; экспертная ревизия между чтением и применением → `409`; отклонение скрывает рекомендацию до новой оценки; preview/intro/прерванные не влияют.
- `test/integration/item_feedback_test.go`: комментарий виден обучаемому, не виден другому обучаемому, не меняет оценку.
- `web/e2e/dds-feedback-recommendation.spec.ts`: комментарий в разборе → виден в ЛК; страница обучаемого → «Применить».

**c10 — `docs: DDS-7 complete`.** `CLAUDE.md`, `README.md`, итог в плане, `slice-planning-dds.md`, `LOG.MD`.

Проверки на каждом шаге — по `CLAUDE.md`: `make verify`, `make verify-web` и `npm run lint` для веба, `check.py` для контрактов, `make test-integration` для миграций и cross-process поведения (c3, c4, c6, c9), browser e2e в c9.

## 4. Риски

- **Мало данных для демонстрации.** Сценариев среднего и сложного уровня почти нет (ДДС-5): после «повысить до medium» назначать почти нечего, до hard рекомендация на демо-данных не дойдёт.
- **Пороги 85/60 и N = 5** — учебные настройки до подтверждения заказчиком; политика версионирована (`policy_version`), замена порогов — новая версия, старые решения остаются читаемыми.
- **Итоговые оценки `needs_review`** (судья недоступен) не входят в основание: при выключенной модели или частых сбоях рекомендация может долго не появляться. UI показывает «k из 5».
- **Одно поле уровня на пользователя.** `users.level` общий для ДДС и 112; сейчас обучаемые двух упражнений — разные учётные записи, но совместная учётка получит один уровень на оба.
