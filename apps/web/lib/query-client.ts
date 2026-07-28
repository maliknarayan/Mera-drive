import { QueryClient } from '@tanstack/react-query';

import { ApiRequestError } from '@/lib/api';

/**
 * TanStack Query is the only cache in the system. Every listing, search result
 * and storage number lives here and nowhere else — the server keeps no copy.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Drive data changes underneath us, so treat it as fresh only briefly
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: true,
        retry: (failureCount, error) => {
          if (error instanceof ApiRequestError) {
            // re-authentication and permission problems never fix themselves
            if (!error.isRetryable) return false;
          }
          return failureCount < 3;
        },
        retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 15_000),
      },
      mutations: {
        retry: false,
      },
    },
  });
}

/** Query keys, centralised so invalidation after a mutation cannot go stale. */
export const queryKeys = {
  meta: ['meta'] as const,
  session: ['session'] as const,
  accounts: ['accounts'] as const,
  storage: ['storage'] as const,
  files: (params: Record<string, unknown>) => ['files', params] as const,
  search: (query: string) => ['search', query] as const,
} as const;
