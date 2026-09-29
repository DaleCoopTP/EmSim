// Thin fetch wrapper (slice 1 plan): every call sends cookies (the
// session lives in emsim_session, ADR-008), targets the API's own
// /api/v1 prefix, and turns a non-2xx response into ApiError built from
// the closed error envelope every endpoint shares
// (design-docs/contracts/openapi.yaml components.schemas.Error). Nothing
// here is React-specific — useMe.ts and later hooks build on this, not
// the other way around.
import type { components } from "./schema";

type ErrorBody = components["schemas"]["Error"];

export class ApiError extends Error {
  readonly status: number;
  readonly code: ErrorBody["error"]["code"];
  readonly details?: Record<string, unknown>;
  readonly requestId: string;

  constructor(status: number, body: ErrorBody) {
    super(body.error.message);
    this.status = status;
    this.code = body.error.code;
    this.details = body.error.details;
    this.requestId = body.request_id;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...init.headers },
  });

  if (!response.ok) {
    const requestId = response.headers.get("X-Request-ID") ?? "";
    let body: ErrorBody;
    try {
      body = (await response.json()) as ErrorBody;
    } catch {
      body = { error: { code: "internal_error", message: response.statusText }, request_id: requestId };
    }
    throw new ApiError(response.status, body);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  // postBlob sends a binary body as-is (dictation audio, ADR-037).
  postBlob: <T>(path: string, body: Blob) => request<T>(path, { method: "POST", body, headers: { "Content-Type": body.type } }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body: JSON.stringify(body) }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};
