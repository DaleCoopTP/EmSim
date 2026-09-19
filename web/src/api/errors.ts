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
      return "Конфликт: логин уже занят или это последний активный администратор.";
    case "not_found":
      return "Не найдено.";
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
  level: "уровень",
  number: "номер РМ",
  workstation_no: "номер РМ",
};

function fieldLabel(field: string): string {
  return fieldLabels[field] ?? field;
}
