import { ApiError } from "./client";
import type { components } from "./schema";

export type Receipt = components["schemas"]["Receipt"];

export interface Command {
  command_id: string;
  expected_seq: number;
  type: "open" | "close" | "set_status" | "add_comment" | "set_card_field" | "call_start" | "call_end" | "control_report" | "answer_incoming" | "end_incoming" | "save_intake_draft" | "dispatch_intake" | "complete_intake";
  payload: Record<string, unknown>;
  client_at?: string;
}

type ErrorBody = components["schemas"]["Error"];

function isReceipt(value: unknown): value is Receipt {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<Receipt>;
  return typeof candidate.command_id === "string" && (candidate.outcome === "applied" || candidate.outcome === "rejected");
}

// Command decisions deliberately use non-2xx status codes for rejected
// attempts, but still return a durable Receipt. Only transport/auth/
// ownership failures use the ordinary Error envelope and throw ApiError.
export async function executeCommand(itemId: string, command: Command): Promise<Receipt> {
  const response = await fetch(`/api/v1/items/${encodeURIComponent(itemId)}/actions`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(command),
  });

  let body: unknown;
  try {
    body = await response.json();
  } catch {
    body = undefined;
  }
  if ((response.ok || response.status === 409 || response.status === 422) && isReceipt(body)) return body;

  const requestId = response.headers.get("X-Request-ID") ?? "";
  const errorBody = body as ErrorBody | undefined;
  if (errorBody?.error?.code) throw new ApiError(response.status, errorBody);
  throw new ApiError(response.status, {
    error: { code: "internal_error", message: response.statusText || "Command failed" },
    request_id: requestId,
  });
}
