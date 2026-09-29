// Russian labels for what the administrator's screens show: background
// task kinds and states (ADR-033) and audit-log actions (ADR-038). An
// unknown key is shown as it is, so a new kind or action needs no code
// change to appear — only to be translated.

export const taskStatusLabels: Record<string, string> = {
  waiting: "ожидает",
  pending: "в очереди",
  leased: "выполняется",
  done: "готово",
  failed: "ошибка",
  dead_letter: "исчерпаны попытки",
  cancelled: "отменена",
};

export const taskKindLabels: Record<string, string> = {
  "backup.run": "Резервная копия",
  "audit.prune": "Очистка журнала аудита",
  "lesson.close": "Закрытие занятия",
  "assessment.evaluate": "Автооценка",
  "report.build": "PDF-отчёт",
  "caller.reply": "Ответ заявителя",
  "caller.warmup": "Прогрев модели",
  "system.noop": "Проверка очереди",
};

export const auditActionLabels: Record<string, string> = {
  "auth.login": "Вход",
  "auth.logout": "Выход",
  "auth.bootstrap_admin": "Создан первый администратор",
  "admin.user.create": "Создан пользователь",
  "admin.user.update": "Изменён пользователь",
  "admin.workstations.replace": "Изменены рабочие места",
  "admin.backup.start": "Запущена резервная копия",
  "admin.task.retry": "Повтор задачи",
  "admin.audit.export": "Выгружен журнал аудита",
  "backup.run": "Резервная копия",
  "audit.prune": "Очистка журнала",
  "content.import.classifier": "Импорт классификатора",
  "content.import.intake_catalog": "Импорт каталога 112",
  "content.import.scenarios": "Импорт сценариев",
  "content.import.services": "Импорт служб",
  "scenario.create": "Создан сценарий",
  "scenario.save_draft": "Сохранён черновик сценария",
  "scenario.approve": "Утверждён сценарий",
  "lesson.create": "Создано занятие",
  "lesson.update": "Изменено занятие",
  "lesson.assign": "Назначены карточки",
  "lesson.preview_start": "Запущен предпросмотр",
  "lesson.stop": "Занятие остановлено",
  "item.command": "Команда обучаемого",
  "item.control_report": "Сообщение в отдел контроля",
  "set_status": "Смена статуса",
  "assessment.auto_record": "Автооценка",
  "assessment.expert_revision": "Правка оценки преподавателем",
  "training.recover": "Восстановление после перезапуска",
};

export const auditOutcomeLabels: Record<string, string> = {
  ok: "выполнено",
  rejected: "отклонено",
  error: "ошибка",
};

// Configuration screen (ADR-038): group titles and a Russian meaning for
// each environment variable. A variable without a label is shown by its
// name alone.
export const configGroupLabels: Record<string, string> = {
  database: "База данных",
  security: "Безопасность",
  performance: "Производительность",
  models: "Модели и диктовка",
  backup: "Резервное копирование",
  logging: "Журналирование и аудит",
  process: "Процесс",
};

export const configGroupOrder = ["security", "database", "performance", "models", "backup", "logging", "process"];

export const configParamLabels: Record<string, string> = {
  DATABASE_URL: "Адрес базы данных (без пользователя и пароля)",
  API_LISTEN_ADDR: "Адрес api",
  ADMIN_LISTEN_ADDR: "Адрес служебных проверок и метрик",
  BLOB_ROOT: "Каталог записей и файлов",
  SESSION_TTL: "Срок сеанса",
  COOKIE_SECURE: "Cookie только по HTTPS",
  LOG_LEVEL: "Уровень журнала процесса",
  ASSESSMENT_JUDGE: "Автооценка моделью (llm / off)",
  CALLER_WARMUP: "Прогрев модели заявителя",
  CALLER_OPENING_DELAY: "Задержка первой реплики заявителя",
  DICTATION: "Диктовка (whisper / stub / off)",
  STT_URL: "Адрес распознавания речи",
  STT_LANGUAGE: "Язык распознавания",
  STT_MODEL: "Модель распознавания",
  STT_TIMEOUT: "Предел времени распознавания фразы",
  DICTATION_QUEUE_WAIT: "Ожидание свободного слота диктовки",
  DICTATION_CONCURRENCY: "Одновременных распознаваний",
  DICTATION_MAX_SECONDS: "Длина фразы, секунд",
  WORKER_ID: "Имя worker'а",
  WORKER_POLL_INTERVAL: "Период опроса очереди",
  WORKER_DRAIN_TIMEOUT: "Время на завершение задач при остановке",
  SHORT_CONCURRENCY: "Пул коротких задач",
  LLM_CONCURRENCY: "Пул задач с моделью (оценка)",
  STT_CONCURRENCY: "Пул распознавания",
  REPORT_CONCURRENCY: "Пул отчётов",
  CALLER_CONCURRENCY: "Пул ответов заявителя",
  CALLER_REPLY_TIMEOUT: "Предел времени ответа заявителя",
  JUDGE_TIMEOUT: "Предел времени вопроса судье",
  JUDGE_MAX_TOKENS: "Длина ответа судьи, токенов",
  AUDIT_RETENTION_DAYS: "Срок хранения журнала аудита, дней",
  AUDIT_PRUNE_AT: "Время ежедневной очистки журнала",
  SCHEDULE_TZ: "Часовой пояс расписания",
  CALLER_REPLIER: "Заявитель (llm / stub)",
  LLM_DIALECT: "Диалект API модели",
  LLM_API_KEY: "Ключ API модели",
  CALLER_LLM_URL: "Адрес модели заявителя",
  CALLER_LLM_MODEL: "Модель заявителя",
  CALLER_TEMPERATURE: "Температура заявителя",
  CALLER_TOP_P: "top_p заявителя",
  CALLER_REPEAT_PENALTY: "Штраф за повторы заявителя",
  CALLER_MAX_TOKENS: "Длина реплики заявителя, токенов",
  JUDGE_LLM_URL: "Адрес модели судьи",
  JUDGE_LLM_MODEL: "Модель судьи",
  BACKUP_DIR: "Каталог резервных копий",
  BACKUP_KEEP: "Сколько копий хранить",
  BACKUP_AT: "Время ежедневной копии",
};
