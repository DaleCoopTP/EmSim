# ADR-019. Срез 6: детерминированная авто-оценка, finalizer и экспертные ревизии

Статус: accepted · 2026-09-21 · уточняет ADR-006/013/016; RFC-001 §7.4/§8

## Контекст

Срез 6 добавляет первую реально исполняемую половину конвейера оценки RFC-001 §7.4 — без STT/LLM (срез 9). Нужно зафиксировать: точную формулу балла и нормировки; полный список детерминированных правил ДДС по критериям `rubric.default.json`; как `platform/tasks` поддерживает `waiting`→`pending` (coordinator) и терминальный finalizer при исчерпании попыток/lease; правила экспертной ревизии без auto.

## Решение

### Rubric effective и балл

`effective = merge(rubric.default.json, reference.scoring)` (ADR-013): `weights` переопределяет вес критерия, `critical` добавляет критичность, `disabled` исключает критерий целиком. Далее: исключить `disabled` и `not_applicable`; веса оставшихся критериев нормировать к сумме 100. `unavailable` **не** исключается из набора — его наличие делает автоматический полный балл недостижимым, и оценка получает `status=needs_review`, `score=NULL`, `passed=NULL` (ADR-013/016 A3/A4 — не перенормировать unavailable в полный балл).

Балл одного критерия: `met=1.0`, `not_met=0.0`, `partial=0.5` (единственная частичная величина в MVP — ADR-014/016 не вводят более тонкую шкалу). Итоговый `score = Σ (weight_i / 100) × score_i` по нормированным весам, только если нет ни одного `unavailable`.

Критичность: критерий критичен, если `critical=true` в effective-рубрике, либо `critical_when=refused_profile_incident` и факт совпадает (эталонное решение `accepted`, фактическое — `not_accepted`/`refused`). Критичный критерий в состоянии `not_met` добавляет свой id в `critical_errors` и ограничивает балл сверху `critical_cap`. `passed = score ≥ pass_threshold И critical_errors пуст`. Ручной `score_override` заменяет вычисленный `score`; `passed` пересчитывается по тому же порогу и тем же `critical_errors`.

### Детерминированные правила ДДС

Реализуются в `internal/assessment/dds` над `training.EvidenceBody` + версии сценария `content.Reference`:

| id | правило |
|---|---|
| `T_OPEN` | `open_seconds` vs `timing.open_s`/`partial_until_s`: ≤limit → met, ≤partial → partial, иначе not_met; интервал недостижим (interrupted до открытия) → not_applicable |
| `T_PRIMARY` | то же по `primary_seconds`/`primary_s` |
| `T_COMPLETE` | то же по `work_seconds` (от `primary_at`) /`complete_s`; без primary → not_applicable при interruption, иначе not_met |
| `D_PRIMARY` | `derived.primary_status == reference.primary_decision.status`; критичность `critical_when=refused_profile_incident` |
| `D_COMMENT_REQUIRED` | `comment_required=false` → not_applicable; иначе met при наличии принятого комментария |
| `D_FIELD_CORRECTIONS` (новый, добавлен этим ADR) | `reference.field_corrections` пуст → not_applicable; иначе met/partial/not_met по доле исправлений, каждое подтверждённое `set_card_field` (по `actions[].effect`, ADR-017) до соответствующего `before_status` и итоговым значением в `final_card` |
| `S_SEQUENCE` | `expected_chain` пуст → not_applicable; иначе met/partial/not_met по вхождению `expected_chain` как упорядоченной подпоследовательности `derived.chain` после первичного решения. `events[].expects` в этом срезе не проверяется (перенесено на срез 9/11 — точная проверка реакции обучаемого на конкретное событие требует того же классификатора действий, что и content-level events-редактор) |
| `C_CALL_MADE` | `call.required=false` → not_applicable; иначе met, если требуемый контакт вызван и завершён до `before_status` |
| `C_CALL_LOG` | `call.required=false` → not_applicable; иначе met при непустых `accepted_by`/`summary` завершённого требуемого звонка |
| `G_ADDRESS` | нормализованные компоненты адреса (`street/house/building/apartment`) из письменных полей vs `card.address`; при отсутствии адресных упоминаний — not_applicable; при неоднозначном извлечении — `unavailable` (ADR-013: "неоднозначное извлечение → unavailable", никогда ложный not_met) |
| `D_COMMENT_CONTENT`, `C_CALL_CONTENT`, `C_CALL_LOG_CONTENT`, `G_GRAMMAR` | llm-критерии: `unavailable` в этом срезе (JUDGE=off, срез 9 подключает LLM); `not_applicable`, если их предпосылка (`comment_must_mention`/`call.required`) не задана |

Interruption: маркер сервера в `interruptions[]` делает все `T_*` `not_applicable` для этой карточки (RFC §7.2/§8) и исключает её из будущей рекомендации уровня (срез 10). `close_reason=interrupted` (стоп-триггер) делает `not_applicable` только не достигнутые к моменту прерывания этапы; уже случившиеся факты (например, первичное решение до stop) оцениваются как обычно.

### Задачи и finalizer

`platform/tasks` получает статус `waiting` как первый шаг обычного жизненного цикла (RFC §7.4/ADR-016 A6): close ставит `assessment.evaluate` прямо в `waiting` в той же транзакции, что и evidence; отдельный worker-цикл (coordinator, 2 с, без LISTEN) переводит `waiting→pending` только когда сборка `assessment_inputs` завершена, не расходуя `attempts`. Это требует новых операций хранилища (`WaitingDue`, `PromoteWaitingTx`, `FailWaitingTx`) — они добавляются в `internal/platform/tasks` как обобщённый примитив, не специфичный для assessment, потому что RFC §11/tasks.schema.json уже описывает тот же паттерн для будущего `advice.generate`.

Исчерпание попыток или потеря lease на последней попытке не должны молча оставить карточку без какой-либо оценки: общий finalizer (зарегистрированный в `Recovery` по kind) в этом случае атомарно пишет `auto rev=1 needs_review` (правила из уже запечатанного input, llm-часть `unavailable`) вместе с терминальным `failed`/`dead_letter` статусом задачи — это ровно ADR-016 A2's "исчерпание попыток через общий finalizer handler/reaper". Если `assessment_inputs` ещё не существует (сбой ещё на этапе подготовки), auto не создаётся — карточка остаётся без автооценки, но ручная оценка по evidence уже доступна.

### Экспертная ревизия

Ручная оценка не требует существования auto или запечатанного input (ADR-006/016 A2). `base_revision=0` при отсутствии итоговой оценки даёт `revision=2` (номер 1 зарезервирован за auto); иначе `revision=base_revision+1`. Первая ручная оценка при `base_revision=0` обязана заполнить все применимые (не `disabled`) критерии рубрики; последующие — только правки, остальное копируется из текущей итоговой ревизии. Экспертная оценка не может содержать `unavailable` (RFC §7.4: "Все unavailable должны быть разрешены преподавателем"). Она атомарно отменяет любую незавершённую `evaluate` (`waiting`/`pending`/`leased`) и увеличивает `trainee_assessment_state.version` — задачи рекомендаций (срез 10) в этом срезе не ставятся, версия лишь готовит основание для них.

### Отложено явно

- `admin retry` для `assessment.evaluate` (openapi.yaml уже описывает `/admin/tasks/{id}/retry` в общем виде) — не реализуется в срезе 6, до появления реального повода (сбоя, который стоит повторять вручную) вне автотестов.
- Чтение обучаемым своего результата (`GET /items/{id}/assessment` trainee-проекция) — срез 7 вместе с ЛК и отчётами.
- `events[].expects` в `S_SEQUENCE` — срез 9/11.

## Последствия

Единственная точка входа детерминированных правил — evidence + effective rubric, без обращения к живым `items`/`actions` (воспроизводимость по ADR-006). Добавление `D_FIELD_CORRECTIONS` не меняет `rubric.default.json`'s `version` — рубрика ещё не выдавалась ни одному прохождению (срезы 1–5 не считали оценок), это не правка выпущенной версии, а завершение первой. Общий `waiting`/finalizer-примитив в `platform/tasks` пригодится будущим `advice.generate`/`recommendation.compute` без повторной реализации.
