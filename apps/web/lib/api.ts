import type { ApiEnvelope, ApiError, ApiMeta, ErrorCode } from '@sangamdrive/shared';

/**
 * Browser-side client for the SangamDrive API.
 *
 * Auth is a cookie, so every call sends credentials. State-changing calls carry
 * the double-submit CSRF token the API sets alongside the session cookie.
 */

const API_BASE_URL = (
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080'
).replace(/\/$/, '');

const CSRF_COOKIE = 'sangam_csrf';
const CSRF_HEADER = 'X-CSRF-Token';

/** An error returned by the API, or a transport failure normalised to look like one. */
export class ApiRequestError extends Error {
  readonly code: ErrorCode;
  readonly status: number;
  readonly details?: Record<string, unknown>;
  readonly accountId?: string;
  readonly requestId?: string;

  constructor(error: ApiError, status: number, requestId?: string) {
    super(error.message);
    this.name = 'ApiRequestError';
    this.code = error.code;
    this.status = status;
    this.details = error.details;
    this.accountId = error.account_id;
    this.requestId = requestId;
  }

  /** True when the user must sign in again. */
  get isAuthError(): boolean {
    return this.code === 'unauthorized' || this.status === 401;
  }

  /** True when retrying the identical request could plausibly succeed. */
  get isRetryable(): boolean {
    return this.code === 'rate_limited' || this.code === 'upstream_unavailable';
  }
}

export interface ApiResult<T> {
  data: T;
  meta?: ApiMeta;
}

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
  /** Absolute URL query parameters. Undefined and null values are dropped. */
  query?: Record<string, string | number | boolean | undefined | null>;
}

/** Issue a request and unwrap the response envelope. */
export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<ApiResult<T>> {
  const { body, query, headers, ...init } = options;
  const method = (init.method ?? 'GET').toUpperCase();

  const requestHeaders = new Headers(headers);
  requestHeaders.set('Accept', 'application/json');

  if (body !== undefined && !(body instanceof FormData)) {
    requestHeaders.set('Content-Type', 'application/json');
  }
  if (method !== 'GET' && method !== 'HEAD') {
    const token = readCookie(CSRF_COOKIE);
    if (token) requestHeaders.set(CSRF_HEADER, token);
  }

  let response: Response;
  try {
    response = await fetch(buildUrl(path, query), {
      ...init,
      method,
      headers: requestHeaders,
      credentials: 'include',
      body: serialiseBody(body),
    });
  } catch {
    throw new ApiRequestError(
      {
        code: 'upstream_unavailable',
        message: 'Could not reach the SangamDrive server. Check your connection and try again.',
      },
      0,
    );
  }

  if (response.status === 204) {
    return { data: undefined as T };
  }

  const envelope = await parseEnvelope<T>(response);

  if (!response.ok || envelope.error) {
    throw new ApiRequestError(
      envelope.error ?? { code: 'internal_error', message: 'An unexpected error occurred.' },
      response.status,
      envelope.request_id,
    );
  }

  return { data: envelope.data as T, meta: envelope.meta };
}

/** Convenience wrapper for callers that do not need response metadata. */
export async function apiGet<T>(path: string, query?: RequestOptions['query']): Promise<T> {
  const { data } = await apiRequest<T>(path, { query });
  return data;
}

export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  const { data } = await apiRequest<T>(path, { method: 'POST', body });
  return data;
}

export async function apiPatch<T>(path: string, body?: unknown): Promise<T> {
  const { data } = await apiRequest<T>(path, { method: 'PATCH', body });
  return data;
}

export async function apiDelete<T>(path: string): Promise<T> {
  const { data } = await apiRequest<T>(path, { method: 'DELETE' });
  return data;
}

/** Absolute URL for links the browser must navigate to directly, such as OAuth. */
export function apiUrl(path: string, query?: RequestOptions['query']): string {
  return buildUrl(path, query);
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = new URL(path.startsWith('/') ? path : `/${path}`, API_BASE_URL);
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== null) {
      url.searchParams.set(key, String(value));
    }
  }
  return url.toString();
}

function serialiseBody(body: unknown): BodyInit | undefined {
  if (body === undefined) return undefined;
  if (body instanceof FormData || body instanceof Blob || typeof body === 'string') {
    return body;
  }
  return JSON.stringify(body);
}

async function parseEnvelope<T>(response: Response): Promise<ApiEnvelope<T>> {
  try {
    return (await response.json()) as ApiEnvelope<T>;
  } catch {
    // a proxy or gateway can return HTML on failure
    return {
      error: {
        code: 'internal_error',
        message: `The server returned an unexpected response (HTTP ${response.status}).`,
      },
    };
  }
}

function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null;

  const match = document.cookie
    .split('; ')
    .find((entry) => entry.startsWith(`${name}=`));

  return match ? decodeURIComponent(match.slice(name.length + 1)) : null;
}
