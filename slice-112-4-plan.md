# Срез 112-4 — полный кейс и оповещение служб по инструкции

Дата: 2026-09-24. Статус: план.

Основание: `slice-planning-112.md` §7 (пересмотр 24.09.2026) и
[ADR-023](design-docs/adr/023-operator112-notify-and-save.md). По инструкции
оператора 112 (Рис. 37, 44, 45; [image56.png](docs/reference-ui/112-instruction/image56.png))
список служб формируется автоматически по типу происшествия, оператор может
его дополнить, а «Сохранить» открывает окно «Список оповещаемых служб»; одно
действие «оповестить и сохранить карточку» оповещает весь список. Это
заменяет прежний план 112-4 («отправка нескольким службам, снимок на каждую»).

## Пользовательский результат

Обучаемый в одном задании принимает вызов, опрашивает заявителя, заполняет
основную карточку, выбирает тип происшествия и профильные карты, проверяет
автоматически сформированный список служб и одним действием «оповестить и
сохранить карточку» оповещает все службы списка. Отдельно, без изменения
финала, кейсы `card_only` из 112-3 получают тот же финал вместо
`review_service_selection` → `complete_profile_case`. Преподаватель видит
итоговый список оповещённых служб, время и снимок отправленной карточки.

## Модель и правила

1. **Новый режим сценария `intake112.mode = "full_case"`.** Объединяет
   разговор 112-2 (`call` + `dialogue`) и каталог типов/карт 112-3 в одной
   версии. Не содержит `recipient_services`/`reference.recipient_service`
   (это поле относится только к старому `incoming_call`); эталон содержит
   `reference.expected_types` и `reference.expected_services` для ручного
   разбора. Валидация — как у `incoming_call` для диалога и как у `card_only`
   для каталога; запрещённые комбинации отклоняются с понятной ошибкой.
2. **Единая команда `notify_services {services[], reason}`.** Доступна для
   `card_only` и `full_case`. Требует сохранённый черновик (`HasSavedDraft`)
   и хотя бы один выбранный тип происшествия. `services` — непустой список
   без дублей, каждый код — из правил каталога занятия (уже назначенные или
   добавленные вручную, как в `review_service_selection`). `reason`
   обязателен, если список отличается от `SuggestedServices` — то же
   правило, что уже действует в `review_service_selection`; их проверка
   выносится в общую функцию `internal/training/operator112`, не
   дублируется. После успешной команды черновик закрыт: `save_intake_draft`
   и `add_incident_type`/`remove_incident_type` отклоняются
   (`transition_not_allowed`).
3. **Финал.** Для новых `card_only` и всех `full_case`:
   `... → save_intake_draft → notify_services → complete_intake`.
   `complete_intake` требует состоявшегося оповещения; для `full_case`
   дополнительно требует `CallStatus ∈ {ended}`, для `card_only` —
   `CallStatus == not_applicable` (как сейчас). «Вернуться к заполнению» в
   UI — закрытие окна без команды; черновик остаётся открытым.
   `review_service_selection`/`complete_profile_case` остаются рабочими
   только для `card_only` items, начатых до этого среза (совместимость),
   и не предлагаются новым прохождениям.
4. **Выбор финала фиксируется при создании item.** `IntakeState.Finale`:
   `"notify"` для новых `card_only` и `full_case`; пусто/отсутствует —
   старое поведение (`review_service_selection` для `card_only`,
   `dispatch_intake` для `incoming_call`). Существующие незакрытые items
   без поля не меняют маршрут задним числом.
5. **Хранение оповещения.** Новая неизменяемая таблица
   `intake_notifications (item_id PK, action_id UNIQUE, services jsonb,
   suggested jsonb, reason text, card_snapshot jsonb, notified_at timestamptz)`
   с триггером `reject_immutable_change`, по образцу `intake_dispatches`.
   Один снимок на оповещение: основная карта + активные профильные карты на
   момент оповещения; неактивные (удалённые) ответы в снимок не входят —
   как уже делает `EvidenceBody` для `IntakeState.InactiveProfiles`.
   Отдельных записей на каждую службу нет.
6. **Evidence.** `EvidenceBody` получает поле `notification *IntakeNotification`
   рядом с существующим `dispatch *IntakeDispatch`. Ровно одно из двух полей
   заполнено (или оба пусты для незавершённых/прерванных карточек). Старые
   evidence с одним `dispatch` продолжают читаться и валидироваться без
   изменения схемы `dispatch`.
7. **`dispatch_intake` не расширяется.** Остаётся только для прежних версий
   `incoming_call` с ровно одной `recipient_services`; для `full_case`
   команда отклоняется (`transition_not_allowed`).
8. **Повтор и stop.** Повтор `notify_services` по тому же `command_id`
   возвращает прежнюю квитанцию и не создаёт вторую запись
   (`UNIQUE(item_id)`/`UNIQUE(action_id)`, как у `intake_dispatches`). Stop
   после `notify_services`, но до `complete_intake`, сохраняет запись
   оповещения в evidence через общий барьер `lesson.close`.

## Порядок реализации

1. **Контракты.** `scenario.schema.json`/`scenario-file.schema.json` —
   `full_case`; `openapi.yaml` — команда `notify_services`, схема
   `IntakeNotification`, поле в проекциях item/monitor;
   `evidence.operator112.schema.json` — поле `notification`; генерируемые
   типы клиента. `python3 design-docs/contracts/check.py` проходит.
2. **Хранение и чистые правила.** Миграция `00015_operator112_notifications.sql`
   (таблица, `actions_type_check`, Down отказывает при наличии данных — по
   образцу 00011/00014). `internal/training`: `CommandNotifyServices`,
   `IntakeNotification`, `IntakeState.Finale`, `Store.InsertIntakeNotification`/
   `IntakeNotificationByItem`. `operator112/rules.go` и `profile_rules.go`:
   общая функция проверки списка служб и `reason`; ветки `notify_services` и
   обновлённый `complete_intake`/`complete_profile_case`. Evidence — поле
   `Notification`. Все изменения состояния, аудит и уведомление — в одной
   транзакции команды, как для существующих команд.
3. **`full_case`.** `internal/content`: валидация нового режима (диалог +
   каталог, без recipient_services). `training/service.go`: каталог нужен на
   старте и для `full_case` (расширить проверку `needsCatalog`); создание
   item — состояние звонка `ringing`, каталог, пустые `incident_types`/
   `profiles`, `Finale = "notify"`. Один seed-кейс:
   `seed/scenarios/pilot-112-full-gas-road-traffic-fire-01.json` — разговор
   + ожидаемые типы 104/101 и службы; `emsim import seed` идемпотентен.
4. **API и интерфейс.** Выдача задания и монитор показывают каталог, активные
   карты и оповещение без эталона (предложенные/добавленные вручную коды,
   причина, время, снимок). `Operator112ProfileCase.tsx`: «Сохранить» → окно
   «Список оповещаемых служб» → «оповестить и сохранить карточку» /
   «вернуться к заполнению» → «Завершить»; старые items без `Finale`
   сохраняют прежние кнопки. `Operator112Workplace.tsx`: экран входящего
   вызова переводится на вёрстку карточки 112-3 (общие блоки, не копии) и
   получает тот же финал для `full_case`; для старого `incoming_call` —
   прежняя ручная отправка одной службе. `LessonDetail.tsx`: запись
   оповещения в мониторе и разборе.
5. **Проверка и документы.** README, `web/README.md`, `seed/README.md` —
   фактическое поведение; `LOG.MD` — запись по каждому коммиту.

## Definition of Done

- Комбинированный `full_case` кейс с разговором проходит от вызова до
  оповещения 104 и 101 одним действием; запись оповещения содержит обе
  службы, отметку предложенных/добавленных вручную и один снимок карточки.
- «Вернуться к заполнению» не создаёт записи; повтор `notify_services` не
  создаёт вторую; stop после оповещения, но до завершения, сохраняет запись
  в evidence.
- Кейсы `card_only`, назначенные после этого среза, завершаются тем же
  действием; ранее начатые `card_only` без `Finale` проходят прежним
  маршрутом без ошибок.
- Старые разговорные (`incoming_call`) прохождения и их evidence с одним
  `dispatch` читаются без изменений; регрессия ДДС проходит.
- `dispatch_intake` для `full_case` отклоняется; `notify_services` для
  `incoming_call` отклоняется.

## Открытый вопрос

Повторное сохранение карточки и повторное оповещение служб после первого
`notify_services` в инструкции не описаны (§3.1, §15 плана 112). До ответа
заказчика карточка после `notify_services` закрыта для правок; этот срез
такое расширение не реализует.

## Проверки реализации

`gofmt`, `make verify`, `python3 design-docs/contracts/check.py`,
`make verify-web`, `cd web && npm run lint`, `make test-integration`,
`cd web && npm run test:e2e`, `git diff --check`, ручная проверка на
`docker compose up --build`.
