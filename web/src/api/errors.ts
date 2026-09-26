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
    case "forbidden":
      return "Недостаточно прав.";
    case "conflict":
      return "Конфликт состояния. Обновите страницу и повторите действие.";
    case "not_found":
      return "Не найдено.";
    case "stale_draft":
      return "Черновик изменён с момента загрузки. Обновите страницу, чтобы не потерять чужие правки.";
    case "has_blocking_issues":
      return "Сначала устраните ошибки проверки (вкладка «Проверка»).";
    case "unsupported_for_editor":
      return "Редактор 112-7 работает только со сценариями «полный кейс» + «ИИ-заявитель».";
    case "validation_failed":
      if (field === "workstation_no") {
        if (reason === "required") return "Укажите номер рабочего места.";
        if (reason === "unknown") return "Рабочее место с таким номером не найдено.";
        if (reason === "inactive") return "Это рабочее место отключено.";
      }
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
};

function fieldLabel(field: string): string {
  return fieldLabels[field] ?? field;
}
