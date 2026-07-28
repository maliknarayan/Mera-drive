'use client';

import type { ApiError, ConnectedAccount, StorageSummary } from '@sangamdrive/shared';
import { summariseAccounts } from '@sangamdrive/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';

import { ApiRequestError, apiDelete, apiRequest } from '@/lib/api';
import { queryKeys } from '@/lib/query-client';

export interface AccountsState {
  accounts: ConnectedAccount[];
  /** Per-account failures from the fan-out, keyed by account id. */
  failures: Map<string, ApiError>;
  summary: StorageSummary;
  isPending: boolean;
  isFetching: boolean;
  error: ApiRequestError | null;
  refetch: () => void;
}

/**
 * Connected accounts with live storage figures.
 *
 * A partial failure is a 200 with `meta.errors`, so the healthy Drives render
 * while the unhealthy ones get an inline action. That is the normal case for
 * anyone with several accounts, not an edge case.
 */
export function useAccounts(): AccountsState {
  const query = useQuery({
    queryKey: queryKeys.accounts,
    queryFn: () => apiRequest<ConnectedAccount[]>('/api/v1/accounts'),
  });

  // stable identity so the memos below only recompute on a new response
  const accounts = useMemo(() => query.data?.data ?? [], [query.data]);

  const failures = useMemo(() => {
    const map = new Map<string, ApiError>();
    for (const failure of query.data?.meta?.errors ?? []) {
      if (failure.account_id) map.set(failure.account_id, failure);
    }
    return map;
  }, [query.data]);

  const summary = useMemo(() => summariseAccounts(accounts), [accounts]);

  return {
    accounts,
    failures,
    summary,
    isPending: query.isPending,
    isFetching: query.isFetching,
    error: query.error instanceof ApiRequestError ? query.error : null,
    refetch: () => void query.refetch(),
  };
}

/** Disconnect an account and revoke its Google grant. */
export function useDisconnectAccount() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (accountID: string) => apiDelete<void>(`/api/v1/accounts/${accountID}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.accounts }),
  });
}

/** Persist the display order of the account cards. */
export function useReorderAccounts() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (accountIDs: string[]) =>
      apiRequest<void>('/api/v1/accounts/order', {
        method: 'PATCH',
        body: { account_ids: accountIDs },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.accounts }),
  });
}
