# Срез 5 — телефон и запись доклада

## Summary

Реализовать минимальный браузерный симулятор РТУ Т16Р: выбор контакта/DSS-кнопки, набор номера, один активный исходящий звонок, greeting, запись доклада, mute, завершение, ack и заполнение отработки. Реальный SIP, многолинейность, STT и точное копирование интерфейса Т16Р не входят.

Записи хранятся как content-addressed файлы на общем Docker volume; PostgreSQL хранит метаданные и связь с карточкой. Один API-процесс обслуживает всех пользователей конкурентно. Целевые браузеры: Chrome, Edge и Яндекс Браузер.

Реализацию выполнять моделью `gpt-5.6-terra` с reasoning effort `high`, последовательно по коммитам ниже.

## Контракты и правила

- `call_start {contact}` разрешён после открытия карточки только для контакта сценария. На карточке может быть не более одного активного звонка; последовательных звонков может быть несколько.
- `call_end {call_id, accepted_by, summary, recording}` завершает только активный звонок. Поля отработки обязательны и не могут быть пустыми.
- `recording` равен `null` при недоступном микрофоне либо содержит неизменяемый `{sha256,size,mime}`. Для браузерной записи основной формат — `audio/webm`/Opus, максимум 10 МиБ.
- Обязательный звонок должен быть завершён с требуемым контактом до `reference.call.before_status` и до закрытия карточки. Активный звонок блокирует `close`.
- Новые rejection-коды команд: `call_required`, `call_in_progress`, `call_not_active`. Неизвестный контакт или некорректный манифест дают `invalid_payload`.
- `call_start`, `call_end`, action, audit, изменение `seq/log_seq` и строка `calls` фиксируются атомарно. Replay возвращает первоначальную квитанцию и тот же `call_id`.
- При закрытии/stop для объявленных записей фиксируется deadline `closed_at + recording_grace_s`. Evidence содержит звонки и манифесты, но поздний upload его не изменяет. Активный при stop звонок сохраняется только как факт начала.
- Состояния загрузки: `absent`, `awaiting`, `ready`, `expired`. В UI `expired` отображается как итог `missing`; STT-поля до среза 9 не заполняются.
- Upload до закрытия не ограничен deadline; после закрытия новый файл принимается до deadline. Повтор уже принятого файла всегда успешен, другой файл — `recording_conflict`, новый файл после срока — `recording_deadline_passed`.
- Преподаватель-владелец занятия может скачать готовую запись только после завершения/прерывания карточки. Администратор, другой преподаватель и обучаемые доступа не имеют.

### API

- Активировать `PUT /api/v1/items/{itemId}/calls/{callId}/recording`.
- Добавить `GET` на тот же путь для защищённого прослушивания преподавателем.
- Добавить `GET /api/v1/items/{itemId}/contacts/{contactKey}/phrases/{phrase}`, где `phrase ∈ {greeting,ack}`.
- Расширить `GET /items/{itemId}`: типизированные `calls[]`; контакты карточки получают тексты реплик и nullable URL подготовленных файлов.
- Оставить импорт голосовых файлов административной CLI-операцией, без пользовательского UI.

### Хранение

- Миграция `00008`: `blobs`, `voice_assets`, `calls`; в `calls` добавить `reaction_at_call`; частичный UNIQUE запрещает два незавершённых звонка одной карточки.
- Файлы хранить по SHA-256 под `BLOB_ROOT`, через временный файл и атомарный rename. Имена и пути клиента не использовать.
- Перед фиксацией upload сверять размер, hash, MIME и magic bytes WebM/Ogg/WAV. Возможный content-addressed orphan после сбоя БД безопасен и в этом срезе не очищается.
- Docker volume `blob-data` подключить к seed, API и worker. Это общий диск процессов, а не несколько API-узлов.
- Подготовленные реплики импортировать из `seed/voice-assets/manifest.json`; повтор идентичного импорта — no-op, изменение существующей immutable-привязки — конфликт.

## План коммитов

### C1 — `docs: define slice 5 phone and recording contracts`

- Добавить этот план, обновить ADR-005/RFC, OpenAPI, целевую SQL-схему и evidence schema.
- Зафиксировать команды, состояния, rejection-коды, media endpoints, авторизацию, deadline и семантику `expired → missing`.
- Добавить контракт manifest голосовых файлов и положительные/отрицательные проверки в `contracts/check.py`.
- Не исправлять техдолг среза 4 в этом коммите.

### C2 — `storage: add call and blob persistence`

- Добавить миграцию 00008 и поднять expected schema version.
- Реализовать типы и PostgreSQL-порты для blobs, voice assets и calls.
- Покрыть ограничения: один активный звонок, корректная форма manifest/state, FK и идемпотентное переиспользование blob по hash.
- Добавить `reaction_at_call`, необходимый evidence.

### C3 — `content: import prepared phone voice assets`

- Реализовать файловый blob-адаптер, `BLOB_ROOT`, безопасную staging/content-addressed запись.
- Добавить `emsim import voice-assets --actor ... <manifest>` и необязательный финальный шаг voice-assets в `import seed`.
- Валидировать сценарий/версию/контакт, наличие greeting+ack, WAV/OGG MIME, размер и digest.
- Использовать генерируемые тестовые WAV-файлы; реальные реплики пока не добавлять.
- Настроить Docker volume и права пользователя контейнера `65532`.

### C4 — `training: implement DDS phone rules and call evidence`

- Расширить exercise context данными call policy и текущими звонками, не связывая домен с HTTP/PostgreSQL.
- Реализовать правила `call_start`, `call_end`, обязательного звонка, одного активного звонка и запрета close.
- Добавить типизированную проекцию звонков в evidence и `derived.call_count`.
- Unit-тестами покрыть корректные и запрещённые последовательности, `recording=null`, неправильный контакт и stop с активным звонком.

### C5 — `training: persist transactional calls and late deadlines`

- Встроить call mutations в существующую транзакцию команд после locks `lesson → run → item → calls`.
- Генерировать `call_id` на сервере и сохранять его в Receipt для replay.
- При выдаче карточки фиксировать публичные контакты сценария; call policy читать через immutable scenario-version port.
- При обычном close и `lesson.close` фиксировать upload deadlines до формирования evidence.
- Расширить чтение карточки типизированными звонками и эффективным состоянием `expired`.
- Проверить гонки `call_start/call_start`, `call_end/close`, `upload/close` и `call_end/stop`.

### C6 — `media: upload and serve phone audio securely`

- Реализовать multipart upload с ограничением 10 МиБ, потоковым SHA-256, проверкой manifest и повторной проверкой владения после staging.
- Сериализовать upload с close через item/call locks; не держать транзакцию во время чтения тела запроса.
- Добавить защищённую выдачу greeting/ack обучаемому и записи преподавателю.
- Готовый повтор возвращает `204` даже после deadline; поздняя новая загрузка переводит эффективное состояние в `expired`.
- Не ставить `stt.transcribe` и другие worker-задачи.
- Добавить HTTP-тесты лимитов, MIME, hash/size mismatch, авторизации и отсутствия утечки пути файла.

### C7 — `web: add minimal T16R phone and recording panel`

- Добавить простую панель: DSS-контакты, дисплей номера, клавиатура, вызов, mute, визуальная громкая связь и завершение.
- Начало звонка: получить `call_id`, запросить микрофон, воспроизвести greeting, затем начать `MediaRecorder`.
- Завершение: остановить запись, воспроизвести ack, запросить «Кто принял»/«Суть сообщения», вычислить SHA-256, отправить `call_end`, затем автоматически загрузить Blob.
- Хранить Blob только в памяти компонента; при сетевом сбое показывать «Повторить загрузку». После reload объявленный manifest остаётся, но утраченные байты не восстанавливаются.
- Явно показывать: permission denied, микрофон отсутствует, MediaRecorder unsupported, awaiting, ready, network error, expired/missing.
- Проверять `MediaRecorder.isTypeSupported("audio/webm;codecs=opus")`, затем `audio/webm`; при отсутствии поддержки разрешать звонок с `recording=null`.
- Не добавлять transfer, hold, conference, реальные линии или детальную стилизацию АРМ/Т16Р.

### C8 — `content: add required-call pilot and final voice replies`

- Добавить новый immutable сценарий `pilot-phone-01`, не изменяя уже импортированные версии существующих сценариев.
- Контакт: руководитель бригады, четырёхзначный учебный номер; обязательный звонок до `accepted`; `must_mention` содержит адрес, тип происшествия и принятые меры.
- В конце среза пользователь выбирает реальные greeting/ack. Перед коммитом привести их к PCM WAV, mono, 16-bit, 16 kHz и подключить через manifest.
- Файлы и manifest включить в offline seed/Docker image; `import seed` должен загрузить их идемпотентно.
- Если реальные файлы ещё не переданы, остановиться перед C8, не подменяя их синтезированными или случайными репликами.

### C9 — `test: accept slice 5 end to end and document`

- Сквозной тест: импорт сценария/реплик → занятие → call_start → phrase download → call_end → upload → close → evidence → instructor recording download.
- Проверить replay upload, конфликт другого файла, позднюю загрузку, deadline, `recording=null`, потерю байтов вкладки и stop с активным звонком.
- Проверить независимые одновременные звонки нескольких обучаемых и отсутствие общего «телефона».
- Выполнить ручной happy path в Chrome, Edge и актуальном Яндекс Браузере, включая отказ в микрофоне и повтор после разрыва сети.
- Обновить README, `.env.example`, seed-инструкцию и append-only `LOG.MD`.
- Финальная проверка: `make verify`, `make test-integration`, `make verify-web`, `cd web && npm run lint`, `make compose-config`, `docker compose build`.

## Commit и workspace policy

- Все коммиты создаются на текущей ветке только с текущей Git-идентичностью: `Matvey Grishin <mathewgr71@gmail.com>` как author и committer.
- Не использовать `--author`, AI-имя, `Co-authored-by` или иные contributor trailers. Перед первым и после каждого коммита проверять `git var GIT_AUTHOR_IDENT`, `git var GIT_COMMITTER_IDENT` и `git show --format=fuller -1`.
- Перед каждым коммитом индексировать только файлы соответствующего C-шагу и проверять staged diff.
- Не включать и не изменять пользовательский `c.txt`.
- Срез 4 считается базовой линией; его известные gaps остаются документированным техдолгом и не смешиваются со срезом 5.
