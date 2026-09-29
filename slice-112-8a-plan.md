# План среза 112-8a: голосовой ввод оператора (диктовка)

Дата: 2026-09-29. Статус: реализуется. Основание: [ADR-037](design-docs/adr/037-operator112-dictation.md), `slice-planning-112.md` §12.

## Контекст

Оператор 112 в чате с ИИ-заявителем сможет диктовать: кнопка микрофона → запись → текст в поле ввода → правка → обычный `send_caller_message`. Всё офлайн. Это первая часть 112-8; озвучка заявителя и голосовой звонок — 112-8b.

## Решения (пользователь, 29.09)

- Первый движок — whisper.cpp; следующие (Vosk) подключаются через тот же порт `Transcriber`.
- Вызов синхронный из api (не через очередь worker), поэтому ADR-037.
- След для разбора — только метка `input: voice`; аудио не хранится.
- Нагрузка — 1–2 одновременных распознавания.

## Коммиты

- c1 docs: ADR-037, этот план, разделение 112-8 на 8a/8b.
- c2 platform: клиент `whisper-server` (`internal/platform/stt/whisper`).
- c3 config: `DICTATION`, `STT_URL`, `STT_LANGUAGE`, `STT_TIMEOUT`, `DICTATION_CONCURRENCY`, `DICTATION_QUEUE_WAIT`, `DICTATION_MAX_SECONDS`.
- c4 training: порт `Transcriber`, семафор, проверки, WAV-валидатор, `POST /items/{id}/dictation`, `dictation` в проекции элемента, OpenAPI.
- c5 training: метка `input` у `send_caller_message` и строки транскрипта; значок в разборе.
- c6 compose: сервис `stt`, `scripts/fetch-stt-model.sh`, `make stt-model`, README.
- c7 web: `wavRecorder.ts`, кнопка микрофона в `CallerChat`.
- c8 тесты: интеграционный и e2e.
- c9 docs: итог, `CLAUDE.md`, `LOG.MD`.

## Проверка

`make verify`, `make verify-web`, `npm run lint`, `python3 design-docs/contracts/check.py`, `make compose-config`, `make test-integration`, `npm run test:e2e`; ручной прогон с whisper на русской речи.

## Риски

- Качество русского на small; модель меняется через `STT_MODEL_FILE`.
- whisper и llama-server делят CPU; полноценный замер — W0.
- Образ whisper.cpp под ARM (Mac) может отсутствовать — тогда нативный запуск.
