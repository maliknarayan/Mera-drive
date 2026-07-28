/** Authentication contract. Mirrors apps/api/internal/auth and routes_auth.go. */

import type { DriveScope } from './account.js';
import type { ErrorCode } from './api.js';

/** What the user is trying to achieve by running the OAuth flow. */
export type AuthIntent = 'login' | 'link' | 'reconnect' | 'upgrade';

export interface SessionResponse {
  user: {
    id: string;
    email: string;
    name: string;
    avatar_url: string;
  };
  expires_at: string;
}

/** Query parameters the OAuth callback appends when redirecting back to the app. */
export interface AuthCallbackParams {
  /** Present on success — the intent that completed. */
  auth?: AuthIntent;
  /** Present on success — the Google account involved. */
  account?: string;
  /** Present on failure — a stable error code. */
  auth_error?: ErrorCode;
  /** Present on failure — a message safe to show the user. */
  auth_message?: string;
}

export const AUTH_CALLBACK_KEYS = [
  'auth',
  'account',
  'auth_error',
  'auth_message',
] as const;

export interface StartAuthOptions {
  intent: AuthIntent;
  scope?: DriveScope;
  /** Required for `reconnect` and `upgrade`. */
  accountId?: string;
  /** Site-relative path to return to. Anything else is ignored by the server. */
  next?: string;
}

/** Human-readable copy for the scope choice, shared by the UI and the docs. */
export const SCOPE_DESCRIPTIONS: Record<DriveScope, { label: string; detail: string }> = {
  'drive.file': {
    label: 'Only files you use here',
    detail: 'SangamDrive sees only the files it creates or that you explicitly open through it.',
  },
  drive: {
    label: 'Full Drive access',
    detail: 'SangamDrive can browse, edit and manage everything in this Drive.',
  },
};
