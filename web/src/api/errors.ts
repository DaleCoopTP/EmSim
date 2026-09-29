import { ApiError } from "./client";

// Human-readable text for an ApiError, keyed by the closed error code
// (openapi.yaml Error.code) and, for 422, by details.field/reason — the
// API's message field is safe operator-facing English, not UI copy.
export function errorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return "Сервер недоступен. Повторите попытку.";
  }
  const field = typeof error.details?.field === "string" ? error.details.field : undefined;
  const reason = typeof error.details?.reason === "string" ? error.details.reason : undefined;
  switch (error.code) {
    case "unauthorized":
      return "Неверный логин или пароль, либо учётная запись отключена.";
    case "rate_limited":
      return "Слишком много попыток входа. Подождите минуту.";
    case "account_locked":
      return "Учётная запись заблокирована после нескольких неверных паролей. Подождите или обратитесь к администратору.";
    case "password_change_required":
      return "Сначала смените пароль.";
    case "forbidden":
      return "Недостаточно прав.";
    case "conflict":
      if (reason === "backup_not_configured") return "Резервное копирование не настроено: у worker'а не задан BACKUP_DIR.";
      if (reason === "backup_in_progress") return "Копия уже создаётся. Дождитесь её завершения.";
      if (reason === "not_retryable") return "Эту задачу нельзя повторить (уже выполнена, отменена или её результат уже записан).";
      return "Конфликт состояния. Обновите страницу и повторите действие.";
    case "not_found":
      return "Не найдено.";
    case "stale_draft":
      return "Черновик изменён с момента загрузки. Обновите страницу, чтобы не потерять чужие правки.";
    case "has_blocking_issues":
      return "Сначала устраните ошибки проверки (вкладка «Проверка»).";
    case "unsupported_for_editor":
      return "Редактор 112-7 работает только со сценариями «полный кейс» + «ИИ-заявитель».";
    case "not_enough_scenarios": {
      const available = typeof error.details?.available === "number" ? error.details.available : undefined;
      const workstation = typeof error.details?.workstation_no === "number" ? ` для РМ № ${error.details.workstation_no}` : "";
      return `Подходящих сценариев${workstation} меньше запрошенного${available === undefined ? "" : ` (доступно: ${available})`}. Выберите больше разделов или уменьшите число карточек.`;
    }
    case "validation_failed":
      if (field?.startsWith("timing.")) return `Проверьте норматив «${fieldLabel(field)}».`;
      if (field?.startsWith("scoring.")) return `Проверьте оценивание: «${fieldLabel(field)}»${reason === "must sum to 100" ? " — сумма весов должна быть 100" : ""}.`;
      if (field === "categories" || field === "count") return "Выберите разделы и число карточек от 1 до 20.";
      if (field === "workstation_no") {
        if (reason === "required") return "Укажите номер рабочего места.";
        if (reason === "unknown") return "Рабочее место с таким номером не найдено.";
        if (reason === "inactive") return "Это рабочее место отключено.";
      }
      if (field === "current_password") return "Текущий пароль указан неверно.";
      if (field === "password" && reason === "same_as_current") return "Новый пароль должен отличаться от текущего.";
      if (field === "password" && reason === "too_short") return "Пароль слишком короткий.";
      return field ? `Проверьте поле «${fieldLabel(field)}».` : "Проверьте введённые данные.";
    default:
      return `Ошибка сервера (${error.code}). Запрос ${error.requestId}.`;
  }
}

const fieldLabels: Record<string, string> = {
  login: "логин",
  password: "пароль",
  full_name: "ФИО",
  role: "роль",
  service_code: "служба",
  number: "номер РМ",
  workstation_no: "номер РМ",
  title: "название",
  mode: "режим",
  level: "уровень",
  assignments: "назначение",
  user_id: "обучаемый",
  scenario_version_ids: "сценарий",
  difficulty_min: "сложность от",
  difficulty_max: "сложность до",
  "timing.open_s": "открытие, с",
  "timing.primary_s": "первичное решение, с",
  "timing.complete_s": "отработка, с",
  "timing.spawn_every_s": "интервал новых карточек, с",
  "scoring.pass_threshold": "порог зачёта",
  "scoring.weights": "веса критериев",
  categories: "разделы",
  count: "число карточек",
};

function fieldLabel(field: string): string {
  if (field.startsWith("scoring.weights.")) return `вес ${field.slice("scoring.weights.".length)}`;
  return fieldLabels[field] ?? field;
}
