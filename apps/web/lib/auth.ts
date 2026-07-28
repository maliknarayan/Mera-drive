'use client';

import type { SessionResponse, StartAuthOptions } from '@sangamdrive/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { ApiRequestError, apiGet, apiPost, apiUrl } from '@/lib/api';
import { queryKeys } from '@/lib/query-client';

/**
 * Absolute URL that begins the Google OAuth flow.
 *
 * This is a full browser navigation rather than a fetch: the server needs to set
 * an HttpOnly state cookie and then hand the user over to Google.
 */
export function googleAuthUrl(options: StartAuthOptions): string {
  return apiUrl('/api/v1/auth/google/start', {
    intent: options.intent,
    scope: options.scope,
    account_id: options.accountId,
    next: options.next,
  });
}

export interface SessionState {
  user: SessionResponse['user'] | null;
  expiresAt: string | null;
  isPending: boolean;
  /** A genuine failure. Being signed out is not one. */
  error: ApiRequestError | null;
}

/**
 * The current session.
 *
 * A 401 is a normal answer here — it means "signed out" — so it resolves to
 * `user: null` instead of surfacing as an error state.
 */
export function useSession(): SessionState {
  const { data, error, isPending } = useQuery({
    queryKey: queryKeys.session,
    queryFn: () => apiGet<SessionResponse>('/api/v1/auth/session'),
    // an expired session should be noticed promptly
    staleTime: 60_000,
  });

  const authError = error instanceof ApiRequestError ? error : null;
  const signedOut = authError?.isAuthError ?? false;

  return {
    user: data?.user ?? null,
    expiresAt: data?.expires_at ?? null,
    isPending,
    error: signedOut ? null : authError,
  };
}

/** Sign out of this browser, or of every browser. */
export function useLogout() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (scope: 'this' | 'all' = 'this') =>
      apiPost<void>(scope === 'all' ? '/api/v1/auth/logout-all' : '/api/v1/auth/logout'),
    onSuccess: () => {
      // every cached query belongs to the session that just ended
      queryClient.clear();
    },
  });
}
