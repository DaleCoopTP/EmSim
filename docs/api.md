# API

Полный контракт — [design-docs/contracts/openapi.yaml](../design-docs/contracts/openapi.yaml) (OpenAPI 3.1). Типы веб-клиента генерируются из этого файла, поэтому клиент и сервер не расходятся. Здесь — общие правила и обзор.

## 1. Общие правила

- **Адрес.** REST/JSON с префиксом `/api/v1`.
- **Аутентификация.** `POST /auth/login` выдаёт cookie сессии (`HttpOnly`, `SameSite=Strict`, `Secure` за HTTPS). Обучаемый передаёт номер рабочего места. Сессия живёт 12 часов, `POST /auth/logout` её отзывает.
- **Роли.** Каждый маршрут доступен определённым ролям; обработчик дополнительно проверяет принадлежность данных.
- **Заголовки ответа.** `X-Request-ID` для поиска в логах, `Cache-Control: no-store`.
- **Ограничения.** Тело JSON — до 64 КБ, аудио — до 10 МБ; частота попыток входа ограничена.
- **Долгие операции** (PDF-отчёт, резервная копия, проверка целостности) выполняются фоновой задачей; готовность видна по отдельному ресурсу или SSE.

### Ошибки

Единый формат:

```json
{"error": {"code": "stale_seq", "message": "Карточка изменилась, обновите страницу", "details": {}}, "request_id": "…"}
```

Коды — закрытый список из 33 значений. Основные:

| Код | Когда |
|---|---|
| `unauthorized`, `forbidden`, `not_found` | нет сессии, нет прав, нет ресурса или он чужой |
| `validation_failed`, `invalid_request` | неверные данные запроса |
| `stale_seq` | карточка изменилась с момента чтения (`expected_seq` устарел) |
| `command_id_conflict` | тот же `command_id` пришёл с другим телом |
| `transition_not_allowed`, `comment_required`, `call_required` | действие нарушает правила упражнения |
| `lesson_stopped`, `item_closed` | занятие остановлено или карточка закрыта |
| `stale_revision`, `stale_draft` | параллельная правка оценки или сценария |
| `maintenance_mode` | включён режим обслуживания, новое занятие не стартует |
| `account_locked`, `password_change_required` | политика входа |
| `dictation_busy`, `dictation_unavailable` | распознавание речи занято или недоступно |

## 2. Группы эндпоинтов

| Группа | Пути | Роль |
|---|---|---|
| Вход | `POST /auth/login`, `POST /auth/logout`, `GET /me`, `POST /me/password`, `GET /system` | все |
| Справочники | `GET /services`, `GET /intake112/catalog`, `GET /scenarios/categories` | преподаватель, администратор (службы) |
| Сценарии | `GET/POST /scenarios`, `GET/PUT /scenarios/{id}`, `GET …/versions`, `GET …/preview`, `POST …/validate`, `…/probe`, `…/preview-runs`, `…/approve` | преподаватель |
| Занятия | `GET/POST /lessons`, `GET/PATCH /lessons/{id}`, `GET /lessons/options`, `GET …/rubric`, `PUT …/assignments`, `POST …/assignments/draw`, `POST …/start`, `POST …/stop` | преподаватель |
| Монитор | `GET /lessons/{id}/monitor`, `GET …/stream` (SSE), `GET …/runs/{runId}/actions` | преподаватель |
| Рабочее место | `GET /my/run`, `GET /my/items`, `GET /items/{id}`, `POST /items/{id}/actions`, `POST /items/{id}/dictation`, `PUT/GET /items/{id}/calls/{callId}/recording`, `GET /items/{id}/contacts/{key}/phrases/{phrase}`, `GET /my/stream` (SSE) | обучаемый |
| Оценка | `GET /lessons/{id}/assessments`, `GET /items/{id}/assessment`, `POST /items/{id}/assessment/revisions` | преподаватель; обучаемый — чтение своей оценки без эталона |
| Отчёты | `GET /lessons/{id}/report`, `GET …/report.csv`, `POST …/report.pdf`, `GET …/report-files`, `GET /reports/{id}/download`, `GET /my/results`, `GET /my/progress` | преподаватель, обучаемый |
| Администрирование | `/admin/users` (+ `/import`, `/{id}`, `/{id}/sessions`), `/admin/workstations`, `/admin/status`, `/admin/audit(.csv)`, `/admin/usage(.csv)`, `/admin/failures(.csv)`, `/admin/config`, `/admin/maintenance`, `/admin/backup`, `/admin/integrity`, `/admin/tasks/{id}/retry` | администратор |

Из 80 операций контракта реализованы 70. Зарезервированы под запланированные возможности и сейчас не обслуживаются:

| Операция | Возможность |
|---|---|
| `POST /scenarios/generate`, `POST /scenarios/{id}/regenerate` | генерация сценариев моделью |
| `DELETE /scenarios/{id}` | архивирование сценария через API (сейчас — флагом в файле сценария) |
| `POST /admin/import/classifier`, `POST /admin/import/tickets` | импорт классификатора и билетов через веб (сейчас — командой `emsim import`) |
| `GET/POST /users/{id}/recommendation`, `GET /users/{id}/progress`, `GET /groups/progress` | рекомендация уровня и прогресс группы |
| `GET /tasks/{id}` | общий статус фоновой задачи |

## 3. Команды обучаемого

Все действия на карточке — `POST /items/{id}/actions`:

```json
{
  "command_id": "0192f7a4-…",
  "expected_seq": 7,
  "type": "set_status",
  "payload": {"status": "arrived", "comment": "Бригада на месте"},
  "client_at": "2026-09-29T10:15:02Z"
}
```

Ответ — квитанция:

```json
{"outcome": "applied", "replayed": false, "action_id": "…", "log_seq": 12, "seq": 8, "item_state": {…}, "server_at": "…"}
```

- `command_id` создаёт клиент. Повтор с тем же `command_id` и телом возвращает исходную квитанцию с `replayed: true` без второго эффекта — и после закрытия карточки или остановки занятия.
- `expected_seq` — версия карточки, которую видел клиент. Если карточка изменилась, команда отклоняется с `stale_seq`; отказ тоже записывается в журнал.
- `outcome: rejected` сохраняет код отказа; исходный HTTP-статус при повторе тот же.

Типы команд:

| Упражнение | Команды |
|---|---|
| ДДС | `open`, `set_status` (статус с комментарием), `call_start`, `call_end`, `answer_incoming`, `end_incoming`, `control_report` (сообщение в отдел контроля по закрытой карточке) |
| Оператор 112 | `answer_incoming`, `hold_incoming`, `resume_incoming`, `ask_intake_question`, `send_caller_message`, `save_intake_draft`, `add_incident_type`, `remove_incident_type`, `notify_services`, `complete_intake`, `mark_no_contact`, `mark_call_dropped` |
| Старые сценарии | `close`, `add_comment`, `set_card_field`, `dispatch_intake`, `review_service_selection`, `complete_profile_case` — только для архивных пилотов и прохождений, начатых до смены правил |

Ответ ИИ-заявителя на `send_caller_message` приходит асинхронно: клиент узнаёт о нём по SSE и перечитывает карточку.

## 4. SSE

- `GET /lessons/{id}/stream` — поток занятия для преподавателя, `GET /my/stream` — поток обучаемого.
- События — только уведомления «ресурс изменился»; источник данных — REST.
- Первое событие — `stream.ready` с курсором. Клиент открывает поток, затем читает состояние через REST, затем применяет накопившиеся события.
- При переподключении клиент передаёт `Last-Event-ID` или `?cursor=`. Неизвестный курсор или перезапуск сервера дают событие `resync` — клиент перечитывает состояние целиком.

Схема событий — [sse-events.schema.json](../design-docs/contracts/sse-events.schema.json).

## 5. Другие контракты

| Файл | Что описывает |
|---|---|
| [schema.sql](../design-docs/contracts/schema.sql) | DDL базы данных |
| [scenario-file.schema.json](../design-docs/contracts/scenario-file.schema.json), [scenario.schema.json](../design-docs/contracts/scenario.schema.json) | файл сценария и тело версии |
| [evidence.schema.json](../design-docs/contracts/evidence.schema.json), [evidence.operator112.schema.json](../design-docs/contracts/evidence.operator112.schema.json) | снимок прохождения ДДС и 112 |
| [assessment-inputs.schema.json](../design-docs/contracts/assessment-inputs.schema.json) | вход оценки |
| [rubric.schema.json](../design-docs/contracts/rubric.schema.json) и `rubric.*.json` | формат рубрики и действующие рубрики |
| [tasks.schema.json](../design-docs/contracts/tasks.schema.json) | фоновые задачи |

Проверка контрактов: `python3 design-docs/contracts/check.py`.
