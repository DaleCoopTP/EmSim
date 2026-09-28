# ДДС-0 и ДДС-1: план по коммитам

## Context

Заказчик 27.09 уточнил роль диспетчера ДДС. Исходная постановка — памятка «Работа на АРМ-112. Памятка для ДДС», стр. 21–32.
- ДДС **не правит** карточку 112.
- Основная работа — **цикл статусов реагирования через «карандаш»**. Комментарий — часть статуса. «Работы завершены», «Отказ» (у 03 — «Завершение без бригады») закрывают карточку.
- Сейчас UI ДДС умеет только «Принять / Не принять / Добавить комментарий / Завершить упражнение / Сохранить округ».
- Workflow сидовых служб обрывается на `accepted`, все сценарии — «дерево во дворе» с `pilot_goal=accept_card`.

Общий план срезов ДДС-0…ДДС-10 уже одобрен. Этот файл детализирует первые два среза.

Решения пользователя: службы — ДДС района и ДДС скорой (03); старые пилоты остаются читаемыми и скрываются из каталога.

### Ключевые факты из кода, на которых построен план

- `content.Workflow{Transitions, CommentRequired, Terminal}` уже есть. У старых пилотных служб `terminal: []`. Это нейтральный признак «старого режима»: он берётся из снимка workflow, а не из эталона, поэтому по нему безопасно различать старое и новое поведение.
- `ImportServices` не меняет существующий код службы: при другом workflow он возвращает `ErrConflict` (`internal/content/import.go`). Поэтому заводим **новые коды служб**.
- `scenarios.status` уже допускает `archived` (`migrations/00004`). `ListScenarios` фильтрует по статусу только при явном фильтре (`internal/content/postgres/store.go` ~стр. 240). Импорт всегда создаёт `approved` (`import.go`).
  - Архивировать миграцией нельзя: на чистой БД сид выполняется после миграций. Значит, архивирует импорт по флагу в файле.
  - `checkAssignableVersion` (`training/service.go:358`) смотрит на `scenario_versions.status`, а не на `scenarios.status`. Назначение пилотов через API (интеграционные тесты) продолжит работать.
- `dds.decideClose` уже вычисляет `CloseReason` и проверяет активный или незавершённый обязательный звонок. Эту логику переиспользуем для закрытия финальным статусом.
- Обучаемый не видит `pilot_goal`. Кнопки в `Workplace.tsx:309–360` сейчас выбираются по `reaction`.
- E2E ДДС (`web/e2e/smoke.spec.ts`) берёт `pilot-phone-01` из каталога и жмёт «Принять» → «Завершить упражнение». Его нужно перевести на новый сценарий.

---

## ДДС-0. Слияние и фиксация решений

**c0 — слияние (не коммит).**
- `main-auligw` на 14 коммитов впереди `main`, отставания нет, обе ветки синхронны с `origin`.
- Команды:

```bash
git checkout main
git merge --ff-only main-auligw
git push origin main
```

- Перед `push` отдельно спрошу подтверждение.
- Работу ДДС ведём в новой ветке `dds` от обновлённого `main`.
- Неотслеживаемые `.claude/`, `AGENTS.md`, `c.txt`, `docs/review-operator112-2026-09-26.md` не трогаем.

**c1 — `docs: DDS role clarification (ADR-030) and DDS slice plan`.**
- `design-docs/adr/030-dds-dispatcher-role.md`:
  - уточнения заказчика: ДДС не правит карточку, неточность → звонок в отдел контроля 112, комментарий — часть статуса, связь с бригадой и заявителем идёт мимо 112;
  - «режим с финальными статусами» (`Workflow.Terminal` непуст): финальный статус закрывает карточку, `close`, `add_comment`, `set_card_field` отклоняются;
  - поправка к ADR-017: он действует только для служб без `terminal`;
  - вычисляемый статус карточки;
  - флаг архивации в файле сценария.
- `slice-planning-dds.md`: общий план ДДС-0…10.
- `slice-dds-1-plan.md`: этот план.
- В `slice-planning.md` пометка, что порядок срезов 8–12 для ДДС заменён.
- `CLAUDE.md`: раздел «Current delivery state» и ссылки.
- `LOG.MD`: запись.
- Проверка: `python3 design-docs/contracts/check.py`.

---

## ДДС-1. Цикл реагирования через «карандаш»

**c2 — `contracts: DDS terminal-status close, card status, archived scenario files`.**
- `openapi.yaml`:
  - `Item.terminal_statuses: ReactionStatus[]` — статусы, сохранение которых закрывает карточку. Пустой массив означает старый режим;
  - `ItemSummary.card_status`: enum `registered | not_notified | in_progress | refused | completed | not_completed`;
  - в описании команд: `set_status` в статус из `terminal_statuses` закрывает карточку, а `close`, `add_comment`, `set_card_field` в этом режиме дают `transition_not_allowed`.
- `scenario-file.schema.json`: необязательное `archived: boolean` на уровне файла.
- `evidence.schema.json` без изменений: `close_reason` остаётся `refused` или `completed`.
- Сгенерировать `web/src/api/schema.d.ts`, прогнать `check.py`.

**c3 — `content: DDS district and ambulance-03 services with full workflows`.**
- `seed/services.json`:
  - **`dds_district_chertanovo`** «ДДС района Чертаново Южное»:
    - переходы: `added→received`, `received→accepted|not_accepted`, `not_accepted→accepted`;
    - из `accepted|responding|arrived|working` можно перейти в любой из `responding, arrived, working, completed, refused`, кроме текущего;
    - `comment_required: [not_accepted, refused]`, `terminal: [completed, refused]`.
  - **`dds_ambulance_03`** «Диспетчерская скорой помощи (03)»:
    - переходы: `received→accepted|completed_without_team`, дальше как у района, но финал `completed | completed_without_team`;
    - `comment_required: [completed_without_team]`, `terminal: [completed, completed_without_team]`.
- `internal/content/validate.go` — проверка workflow при импорте служб:
  - `terminal` и `comment_required` входят в известные статусы;
  - у финального статуса нет исходящих переходов;
  - финальные статусы достижимы из `received`.
- Тесты: `validate_test.go`, `seed_test.go`.

**c4 — `training/dds: saving a terminal status closes the card`.**
- `internal/training/dds/rules.go`:
  - `decideSetStatus`: если `len(item.Workflow.Terminal) > 0` и новый статус финальный, решение получает `Close`. Причина: `refused` для `refused`, `completed` для `completed` и `completed_without_team`. Проверки активного и обязательного звонка берутся из `decideClose` (выносим их в общий хелпер).
  - `decideClose`, `decideAddComment`, `decideSetCardField` при непустом `Terminal` возвращают `RejectTransitionNotAllowed`. Для старых служб поведение не меняется.
- Проверить, что сервис (`training/service.go`, путь применения `Decision.Close`) при закрытии из `set_status` делает всё то же, что при `close`:
  - evidence с этой командой;
  - отмена запланированных событий;
  - выдача следующей карточки;
  - постановка `assessment.evaluate`.

  Если сервис различает типы команд — поправить.
- Тесты `rules_test.go`: полный цикл, закрытие финальным статусом с причиной, отказ при активном звонке, «Не принята» без комментария, у 03 нет `not_accepted`, для старой службы без `terminal` всё по-прежнему.

**c5 — `training: card status projection and terminal_statuses in item views`.**
- Чистая функция `dds.CardStatus(item, now)`:

  | Условие | `card_status` |
  |---|---|
  | `reaction ∈ {added, received}` и `now > deadlines.primary_at` | `not_notified` |
  | `reaction ∈ {not_accepted, refused}` | `refused` |
  | `reaction ∈ {completed, completed_without_team}` | `completed` |
  | карточка прервана или закрыта без завершения | `not_completed` |
  | другой статус реагирования | `in_progress` |
  | до первичного статуса и в пределах срока | `registered` |

  Для 112 поле не заполняется.
- Заполнить `card_status` в `ItemSummary` для `/my/items`, `/items/{id}` и монитора (`training/monitor.go`), а `terminal_statuses` — в `Item` из `item.Workflow.Terminal`.
- Тесты: unit для `CardStatus`, handler-тесты `internal/training/http`.

**c6 — `content: archive flag in scenario files; catalogue hides archived`.**
- `import.go`: `file.Archived` задаёт `scenarios.status`, `archived` или `approved`. Повторный импорт идемпотентен в обе стороны, тело и версии не затрагиваются. Новый метод store `SetScenarioStatus`.
- `ListScenarios`: без явного фильтра статуса исключает `archived`. Фильтр `status=archived` в API оставляем для преподавателя.
- `seed/scenarios/pilot-{tree-01,tree-02,phone-01,z-events-01}.json` получают `"archived": true`.
- Назначение через API не меняем (см. факты выше).
- Тесты: `import` (архивация и возврат), `content/postgres` (фильтр каталога).

**c7 — `seed: DDS full-cycle scenarios for district and ambulance-03`.**
- `seed/scenarios/dds-district-tree-cycle-01.json`:
  - служба `dds_district_chertanovo`, дерево перекрыло проезд, уровень easy;
  - контакт `crew_leader` с уже импортированными WAV-фразами;
  - события `notice` от `crew_leader`: `since: accepted`, `at_s` 20 / 45 / 70 / 100 — «выехали», «на месте», «работаем, вызвали автовышку», «закончили»;
  - `expects.status` у каждого события;
  - `reference`: `primary_decision=accepted`, `expected_chain=[responding, arrived, working, completed]`, `call.required=true` к `crew_leader` до `responding`.
- `seed/scenarios/dds-ambulance-cycle-01.json`: скорая 03, полный цикл.
- Сценарии проходят `emsim import`. Суммарное время укладывается в `complete_s=180` рубрики v1.
- Тесты: `seed_test.go` (импорт сида целиком).

**c8 — `test(integration): DDS response cycle through API and worker`.**
- `test/integration/dds_cycle_test.go`, шаги:
  1. обучаемый со службой `dds_district_chertanovo`;
  2. назначение `dds-district-tree-cycle-01`, старт;
  3. `open` → `accepted` → звонок бригаде → `responding` → `arrived` → `working` → `completed`;
  4. карточка закрыта, `close_reason=completed`, evidence содержит все `set_status` с комментариями;
  5. события после закрытия отменены;
  6. worker создаёт `auto rev=1`.
- Отдельные кейсы:
  - `not_accepted` без комментария → отклонено, с комментарием → принято, потом `accepted`;
  - `close` и `add_comment` в новом режиме отклоняются;
  - stop посреди цикла → `interrupted`;
  - старый `pilot-phone-01` по-прежнему закрывается через `close`.

**c9 — `web: service block with status pencil on the DDS workplace`.**
- `web/src/routes/trainee/Workplace.tsx` и `components/IncidentCard.tsx`:
  - **блок своей службы**: название, текущий статус и время его проставления, «▾» — история `set_status` из `item.actions` (только применённые, со статусом, комментарием и временем);
  - **«✎»** → выпадающий список `allowed_transitions` с русскими подписями статусов, поле комментария, «Сохранить»;
  - отметка «комментарий обязателен» для `not_accepted`, `refused`, `completed_without_team` — это публичное правило памятки, эталона оно не раскрывает;
  - для статусов из `terminal_statuses` — предупреждение «сохранение закроет карточку».
- Кнопки «Принять», «Не принять», «Добавить комментарий», «Завершить упражнение» и форма округа показываются **только** при пустом `terminal_statuses` (старые карточки).
- В очереди карточек — бейдж `card_status`, красный для `not_notified`, `refused`, `not_completed`.
- Телефон и список «Сообщения» пока остаются на месте, их перенесёт ДДС-2.
- `web/src/routes/trainee/Workplace.module.css` или стили АРМ по образцу существующих.

**c10 — `web: card status and status history for the instructor`.**
- `Monitor.tsx`: колонка «Статус карточки».
- `ItemReview.tsx`: история статусов с комментариями в разборе, если её там нет в читаемом виде.

**c11 — `test(e2e): DDS pencil cycle; move smoke off archived pilot`.**
- Новый `web/e2e/dds-cycle.spec.ts`, шаги:
  1. админ создаёт обучаемого со службой `dds_district_chertanovo`;
  2. преподаватель собирает занятие с `dds-district-tree-cycle-01`;
  3. обучаемый открывает карточку, ставит «Принята» через «✎», звонит бригаде, проходит доклады со статусами до «Работы завершены» — карточка закрыта;
  4. монитор показывает `card_status`, разбор показывает историю.
- `smoke.spec.ts` переходит с `pilot-phone-01` на `dds-district-tree-cycle-01`: телефон, запись и повтор загрузки сохраняются, закрытие через финальный статус вместо «Завершить упражнение».

**c12 — `docs: DDS-1 complete`.**
- `README.md`: раздел ДДС (службы, «карандаш», финальные статусы, статус карточки, архивные пилоты).
- `web/README.md`, `seed/README.md`.
- `CLAUDE.md`: «Current delivery state».
- `LOG.MD`: изменённые файлы, фактически выполненные проверки, риски, следующий шаг — ДДС-2.

---

## Verification

- После каждого коммита с кодом: `make verify`. После c2 и c6 ещё `python3 design-docs/contracts/check.py`.
- После c4–c8: `make test-integration`, полный набор, включая старые ДДС- и 112-тесты.
- После c9–c11:

```bash
make verify-web
```

```bash
cd web && npm run lint && npm run test:e2e
```

- Ручная проверка через `docker compose -f compose.yaml -f compose.no-llm.yaml up --build`:
  - обучаемый ДДС района проходит цикл «карандашом»;
  - у обучаемого 03 нет «Не принята»;
  - просроченная карточка в очереди и мониторе красная «Не оповещено»;
  - старые пилоты отсутствуют в каталоге, их прошлые занятия и отчёты открываются.
- Коммиты делаются от имени пользователя (`git config user.*`), без co-author-трейлера (правило `CLAUDE.md`). `push` — только после подтверждения.
