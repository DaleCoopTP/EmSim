# ДДС-2. Связь с бригадой: план по коммитам ([ADR-031](design-docs/adr/031-dds-crew-communication.md))

## Context

ДДС-1 дал цикл статусов через «карандаш». Но доклады бригады, по которым эти статусы ставятся, пока устроены по-пилотному:
- события `notice` в сценарии якорятся на **статусы обучаемого** (`since: accepted`, `at_s` 20/45/70/100). Бригада «выезжает» через 20 с после «Принята», даже если диспетчер ей не звонил;
- доклады показываются общим списком «Сообщения» под карточкой;
- `phone_incoming` есть в схеме сценария, но в коде доставляется так же, как `notice`: входящего звонка нет;
- у контакта нет роли. Ключ `control_112` не проходит `^[a-z_]{2,32}$`;
- эталона содержания комментария к реакции на доклад нет. Он понадобится ИИ-судье ДДС-4.

Памятка (стр. 22, 26, 31–33): статусы хода работ ставятся «по факту получения информации» от бригады. Отдел контроля 112 звонит диспетчеру при нарушениях (нет статуса, неправомерный отказ). Диспетчер сам звонит в отдел контроля, если нужно что-то уточнить. Нарушение №7: «не отвечают на звонки».

Результат среза: доклады бригады приходят по времени происшествия после того, как диспетчер связался с бригадой. Входящие звонки звонят, на них отвечают или их пропускают. Контакты разделены по ролям «бригада», «отдел контроля 112», «заявитель». Справа — панель «Связь с бригадой». Монитор показывает доклады и задержку реакции. Оценка не меняется: `T_PROGRESS`, `C_CALLS` и прочие критерии — это ДДС-3.

### Факты из кода, на которых построен план

- Планировщик `Service.scheduleEventsForAnchor` (`internal/training/service.go:1259`) ставит события по строке `anchor == event.Since`. Якоря выбираются после принятой команды ДДС (`service.go:~1625`): `open` → `opened`, статусы `accepted|responding|arrived|working`. `UNIQUE(item_id,event_key)` защищает от повторного планирования.
- Доставка `tickEvent` (`service.go:~2130`) не различает `notice` и `phone_incoming`. `DeliveredEvent` (`delivered_event.go`) отдаёт обучаемому `text` сразу после доставки.
- Звонки ДДС: `decideCallStart`/`decideCallEnd` (`internal/training/dds/rules.go:309–364`). `call_end` требует `accepted_by`/`summary`. `requiredCallFinished` засчитывает **любой** завершённый звонок контакту. `calls_one_active_per_item_idx` допускает один активный звонок на карточку.
- В таблице `calls` нет направления и связи с событием. Последняя миграция — `00017`.
- В 112 уже есть типы команд `answer_incoming`/`end_incoming` (`actions_type_check`, миграция 00016). У ДДС `Decide` выбирает команды по своему `switch`.
- `content.ProjectContacts` (`internal/content/preview.go:102`) отдаёт обучаемому `key/label/number/voice`, фраз не отдаёт. WAV есть только у `crew_leader` (`seed/voice-assets`). TTS событий нет (`voice_url` всегда null).
- `assessment/dds/rules.go:323` считает звонок по `contact_key` без направления. Входящий звонок бригады засчитался бы как «диспетчер позвонил».
- Версии сценариев неизменяемы. Новые версии сида кладутся рядом файлом `-v2.json`, как у `pilot-112-ai-*-v2.json`.
- `smoke.spec.ts` проходит `dds-district-tree-cycle-01`. Каталог отдаёт последнюю версию, поэтому v2 изменит ход smoke.

### Решения по умолчанию (зафиксировать в ADR-031)

1. **Якорь «после звонка контакту X»**: `since: "call_ended"` + `since_contact: "<contact.key>"`. Момент якоря — первый завершённый **исходящий** звонок этому контакту. Не дозвонился — доклады не придут: evidence покажет недоставленные события, штраф назначит ДДС-3. Якоря по статусам остаются допустимыми для старых версий. Новые сиды их не используют.
2. **Роль контакта**: `contact.role` ∈ `crew | control_112 | applicant | other`, необязательная, по умолчанию `other`. Ключ остаётся свободным (`control` и т. п.). Роль не секретна и отдаётся обучаемому.
3. **Входящий звонок** (`phone_incoming`):
   - после доставки он «звонит» 30 с (`dds.IncomingRingS`, константа). Статус вычисляется из серверного времени, планировщику ничего не нужно;
   - `answer_incoming {event_key}` создаёт запись `calls` с `direction=incoming` и `event_key`;
   - завершение — обычным `call_end`, для входящего `accepted_by`/`summary` могут быть пустыми строками (поля в теле остаются, контракт `call_end` не меняется до ДДС-3);
   - после 30 с `answer_incoming` получает `call_missed`, пропущенный звонок виден в evidence и панели;
   - пока звонок не принят, обучаемый видит только «кто звонит». `text` открывается после ответа, пропущенный звонок текст не раскрывает;
   - запись разговора для входящего не ведётся.
4. **Звонок, закрывающий обязательное требование** (`CallPolicy`, rubric v1 `C_CALL*`), — только исходящий.
5. **`expects.comment_facts: string[]`** — факты, которые должны быть в комментарии к статусу-реакции. В ДДС-2 это только схема и валидация, оценивает ДДС-4. Обучаемому поле не отдаётся: `expects` и так не проецируется.
6. **Реакция на доклад в мониторе** — первый применённый `set_status` после доставки (для входящего — после ответа). Эталон `expects` здесь не используется, монитор показывает фактическую задержку.

---

## Коммиты

**c1 — `docs: DDS-2 plan and ADR-031 (crew communication, incoming calls)`.**
- `design-docs/adr/031-dds-crew-communication.md`: решения 1–6 выше. Поправка к RFC-001 §7.2 о якорях. Что остаётся за ДДС-3 (баллы `T_PROGRESS`, `C_CALLS`) и ДДС-4 (`comment_facts`).
- `design-docs/adr/README.md` — ссылка.
- `slice-dds-2-plan.md` — этот план.
- `LOG.MD` — запись.
- Проверка: `python3 design-docs/contracts/check.py`.

**c2 — `contracts: DDS crew communication — call anchor, contact roles, incoming calls`.**
- `scenario.schema.json`:
  - `event.since` += `call_ended`;
  - `event.since_contact` — обязательно при `call_ended`;
  - `event.expects.comment_facts: string[]`;
  - `contact.role` (enum).
- `evidence.schema.json`: у `calls[]` необязательные `direction` (`outgoing|incoming`, по умолчанию `outgoing`) и `event_key`. Старые evidence валидны.
- `schema.sql`: `calls.direction text NOT NULL DEFAULT 'outgoing' CHECK (...)`, `calls.event_key text`, `UNIQUE (item_id, event_key) WHERE event_key IS NOT NULL`.
- `openapi.yaml`:
  - `answer_incoming {event_key}` для `dds_processing`;
  - `call_end`: `accepted_by`/`summary` необязательны для входящего;
  - новый код отказа `call_missed`;
  - `Call.direction`, `Call.event_key`;
  - `Item.incoming_call: {event_key, from, ring_until} | null`;
  - `DeliveredEvent.text` пуст до ответа на `phone_incoming`, добавлено `answered: boolean`;
  - `ContactPreview.role` и `phrases {greeting, ack}` (текст);
  - `Monitor.rows[].reports[]`: `{item_id, event_key, from, delivery, delivered_at, answered_at, reaction_at}`.
- Сгенерировать `web/src/api/schema.d.ts`, прогнать `check.py`.

**c3 — `content: call_ended anchor, contact roles, comment_facts`.**
- `internal/content/body.go`:
  - `Event.SinceContact`;
  - `EventExpects.CommentFacts`;
  - `Contact.Role` с умолчанием `other`.
- `validate.go`/`validateEvents`:
  - `since=call_ended` требует известный `since_contact`, для остальных `since` поле запрещено;
  - `comment_facts` — непустые строки;
  - известная `role`;
  - `phone_incoming` не допускается с `since=call_ended` на свой же контакт.
- `validate_detailed.go` — те же проверки для редактора, если он читает ДДС-сценарии.
- `preview.go`/`ProjectContacts`: `role` и тексты `greeting`/`ack`.
- Тесты: `validate_test.go`, `preview`-тест: `expects` в проекцию не попадает.

**c4 — `training: call direction and event link; required call counts outgoing only`.**
- Миграция `migrations/00018_dds_incoming_calls.sql`:
  - столбцы `calls`, как в c2;
  - `actions_type_check` уже содержит `answer_incoming`, проверить и не трогать.
- `training.Call`: `Direction`, `EventKey`. Добавить в `postgres/store.go` (insert/select).
- `dds/evidence.go` пишет `direction`/`event_key`.
- `dds.requiredCallFinished`, `activeCall` (без изменений), `assessment/dds/rules.go:323` и `address.go:92` учитывают только `outgoing`. Старые evidence без `direction` читаются как `outgoing`.
- Тесты: `dds/evidence_test.go`, `assessment/dds/rules_test.go` (входящий звонок бригады не засчитан как обязательный).

**c5 — `training: schedule events after the first outgoing call to a contact`.**
- `scheduleEventsForAnchor` принимает вместо строки якорь `{name, contact}` и сопоставляет `call_ended`+`since_contact`.
- В ветке ДДС после принятой команды: `call_end` исходящего звонка → якорь `call_ended` с `contact_key` звонка. Первый звонок фиксирует `anchor_at`, повторный ничего не меняет (`UNIQUE`).
- Тесты: unit и `service`-уровень через существующие фейки. Звонок другому контакту не планирует события. Повторный звонок не сдвигает `due_at`. Входящий звонок бригады не является якорем.

**c6 — `training/dds: incoming calls ring, get answered or missed`.**
- `training.Item` получает `IncomingEvents []IncomingRing{EventKey, From, DeliveredAt}` — доставленные `phone_incoming` карточки. Их загружает `lockForCommand` под той же блокировкой item. `Decide` остаётся чистым.
- `dds.decideAnswerIncoming`:
  - неизвестное или ещё не доставленное событие → `invalid_payload`;
  - уже есть звонок с этим `event_key` → `transition_not_allowed`;
  - `now > DeliveredAt+IncomingRingS` → `call_missed`;
  - активный звонок → `call_in_progress`;
  - карточка `offered` (не открыта) → звонить может, ответить можно только после `open` (как `call_start`), иначе `transition_not_allowed`;
  - успех → `StartCall{Direction: incoming, EventKey, ContactKey: from}`.
- `decideCallEnd`: для входящего `accepted_by`/`summary` необязательны, манифест записи запрещён.
- Проекции (`internal/training/http/handlers.go`):
  - `Item.incoming_call` — последний доставленный, не принятый и не просроченный звонок;
  - `DeliveredEvent`: `text` скрыт, пока не принят; `answered`.
  - Чистая функция `dds.RingingCall(item, now)`.
- Stop и закрытие: непринятый звонок остаётся событием `delivered` без звонка. Финальный статус при звонящем входящем разрешён, при активном разговоре — нет (текущее правило `closePrecondition`).
- Тесты `dds/rules_test.go`: ответ, пропуск по времени, повтор ответа, ответ во время исходящего, `call_end` входящего без полей. `http/handlers_test.go`: текст скрыт до ответа.

**c7 — `training: crew reports and reaction delay in the monitor`.**
- Чистая `dds.ReportReactions(events, calls, actions)`. Для каждого доставленного `notice`/`phone_incoming` она вычисляет `answered_at` и `reaction_at` — первый применённый `set_status` после доставки или ответа.
- `training/monitor.go`: `MonitorRow.Reports` для активных карточек. В HTTP — `Monitor.rows[].reports`, `from` отдаётся как `label` контакта.
- Тесты: unit для `ReportReactions`, handler-тест монитора.

**c8 — `seed: DDS scenarios with crew reports after the call and incoming calls`.**
- `seed/scenarios/dds-district-tree-cycle-01-v2.json`:
  - контакты `crew_leader` (`role: crew`), `control` (`control_112`), `applicant` (`applicant`);
  - доклады `since: call_ended, since_contact: crew_leader`: `e1` «выехали» — `notice`, `e2` «на месте» — `phone_incoming`, `e3` «работаем, вызвали автовышку», `e4` «закончили»;
  - `expects.comment_facts` для `e3`/`e4`.
- `seed/scenarios/dds-ambulance-cycle-01-v2.json` — то же для 03.
- Новый `seed/scenarios/dds-district-pipe-burst-01.json` (памятка, нарушение №6):
  - `control` звонит (`phone_incoming`, `since: offered`, `at_s: 40`, `status_in: [received]`), только если нет первичного статуса;
  - второй звонок `control` с уточнением («жители сообщают о заливе кв. 12») и `comment_facts`;
  - `applicant` доступен для исходящего уточнения.
- `seed/voice-assets/manifest.json`: WAV `crew_leader` для v2-версий. У `control`/`applicant` WAV нет, UI показывает текст фразы.
- Сроки укладываются в `complete_s=180` рубрики v1.
- Тесты: `seed_test.go` (импорт целиком, v1 и v2 сосуществуют).

**c9 — `test(integration): DDS crew communication through API and worker`.**
- Новый `test/integration/dds_comms_test.go`, шаги:
  1. `dds-district-tree-cycle-01` v2;
  2. «Принята» без звонка — доклады не приходят;
  3. звонок `crew_leader` → `e1` доставлен с `anchor_at` = конец звонка;
  4. `e2` звонит → `answer_incoming` → `call_end` без полей;
  5. следующий входящий не принят за 30 с → `call_missed`;
  6. закрытие → в evidence `direction`/`event_key`, пропущенный звонок — событие без звонка;
  7. `auto rev=1`.
- Отдельные кейсы:
  - `pipe-burst`: `control` звонит только при отсутствии первичного статуса;
  - stop во время звонка;
  - повтор `answer_incoming` тем же `command_id` — replay;
  - `pilot-phone-01` и v1-сценарии работают по-старому.

**c10 — `web: crew communication panel with incoming calls on the DDS workplace`.**
- `Workplace.tsx`: вместо списка «Сообщения» правая панель **«Связь с бригадой»**:
  - лента доставленных событий и звонков по времени: подпись контакта с ролью, «Доклад»/«Входящий звонок»/«Исходящий звонок», текст, время, «с опозданием»;
  - баннер входящего по `item.incoming_call`: кто звонит, обратный отсчёт до `ring_until`, «Ответить». После ответа — текст звонящего и «Завершить». Пропущенный звонок в ленте помечен «Пропущен»;
  - `PhonePanel` переезжает в панель. Контакты сгруппированы по ролям: «Бригада», «Отдел контроля 112», «Заявитель», «Прочие». Если WAV нет, показывается текст `greeting`/`ack`.
- Для `legacy`-карточек (пустой `terminal_statuses`) остаётся старый список «Сообщения», чтобы не менять старые экраны.
- Стили по образцу `phone-panel` в `Workplace.module.css`/общих стилях АРМ.

**c11 — `web: crew reports and reaction delay for the instructor`.**
- `Monitor.tsx`: колонка «Связь» — последний доклад, прошедшее время, «нет реакции N с» или «реакция через N с». Пропущенный входящий выделен красным.
- `ItemReview.tsx`: хронология связи из evidence — события, звонки с направлением, пропущенные.

**c12 — `test(e2e): DDS crew communication; smoke on the v2 scenario`.**
- `smoke.spec.ts` на v2: звонок бригаде с записью и повтором загрузки → доклады в панели → ответ на входящий `e2` → статусы → закрытие.
- Новый `web/e2e/dds-comms.spec.ts`: пропущенный входящий и колонка «Связь» в мониторе. Если по времени дешевле, объединить со smoke, как в ДДС-1.
- Обновить эталоны скриншотов (`dds-card`, `live-monitor`) и просмотреть их.

**c13 — `docs: DDS-2 complete`.**
- `README.md`, `web/README.md`, `seed/README.md`: панель связи, входящие звонки, роли контактов, якорь `call_ended`.
- `slice-planning-dds.md`: статус ДДС-2.
- `CLAUDE.md`: раздел «Current delivery state».
- `LOG.MD`: изменённые файлы, фактически выполненные проверки, риски, следующий шаг — ДДС-3.

---

## Verification

- После каждого коммита с кодом: `make verify`. После c2 и c3 ещё `python3 design-docs/contracts/check.py`.
- После c4–c9: `make test-integration`, полный набор, включая ДДС-1 (`TestDDSResponseCycleThroughAPIAndWorker`) и тесты 112. Миграция и транзакции звонков меняются.
- После c10–c12:

```bash
make verify-web
```

```bash
cd web && npm run lint && npm run test:e2e
```

- Ручная проверка через `docker compose -f compose.yaml -f compose.no-llm.yaml up --build`:
  - без звонка бригаде доклады не приходят;
  - после звонка приходят по таймлайну;
  - входящий звонит 30 с, на него можно ответить, иначе он становится пропущенным;
  - в `pipe-burst` отдел контроля звонит при затянутом первичном статусе;
  - монитор показывает задержку реакции;
  - старые пилоты и v1-занятия открываются.
- Коммиты делаются от имени пользователя, без co-author-трейлера. Работа идёт в ветке `dds`. `push` — только после подтверждения.
