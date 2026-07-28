/**
 * Transport contract shared by the Go API and the Next.js client.
 *
 * These types mirror `apps/api/internal/httpx` and `apps/api/internal/apperr`.
 * Keep them in lockstep — a contract test in a later phase enforces it.
 */

/** Stable machine-readable error identifiers. Mirrors apperr.Code. */
export const ERROR_CODES = [
  'bad_request',
  'validation_failed',
  'unauthorized',
  'csrf_invalid',
  'forbidden',
  'not_found',
  'conflict',
  'payload_too_large',
  'rate_limited',
  'internal_error',
  'reauth_required',
  'insufficient_scope',
  'quota_exceeded',
  'upstream_unavailable',
] as const;

export type ErrorCode = (typeof ERROR_CODES)[number];

export interface ApiError {
  code: ErrorCode;
  message: string;
  details?: Record<string, unknown>;
  /** Set when the failure belongs to one specific connected account. */
  account_id?: string;
}

/** Pagination cursors and partial-failure reporting for fan-out endpoints. */
export interface ApiMeta {
  next_page_token?: string;
  count: number;
  /**
   * Populated when some connected accounts failed while others succeeded.
   * The UI surfaces these as per-account warnings rather than failing the page.
   */
  errors?: ApiError[];
}

/** Every JSON response body. Exactly one of `data` and `error` is present. */
export interface ApiEnvelope<T> {
  data?: T;
  meta?: ApiMeta;
  error?: ApiError;
  request_id?: string;
}

export interface BuildInfo {
  version: string;
  commit: string;
  built: string;
}

export interface ServerMeta {
  name: string;
  environment: 'development' | 'production';
  build: BuildInfo;
}

/** Error codes that mean the user must take an action on a specific account. */
export const ACCOUNT_ACTION_CODES: readonly ErrorCode[] = [
  'reauth_required',
  'insufficient_scope',
  'quota_exceeded',
];

export function isAccountActionRequired(error: ApiError): boolean {
  return ACCOUNT_ACTION_CODES.includes(error.code);
}

/** Whether retrying the identical request could plausibly succeed. */
export function isRetryable(error: ApiError): boolean {
  return error.code === 'rate_limited' || error.code === 'upstream_unavailable';
}
