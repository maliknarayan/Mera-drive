'use client';

import type { ServerMeta } from '@sangamdrive/shared';
import { useQuery } from '@tanstack/react-query';
import { AlertCircle, CheckCircle2 } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { ApiRequestError, apiGet } from '@/lib/api';
import { queryKeys } from '@/lib/query-client';

/**
 * Live connectivity check against the Go API. It exists to prove the full
 * request path — browser to Fiber, envelope unwrapping, error mapping — before
 * any real feature depends on it.
 */
export function ApiStatus() {
  const { data, error, isPending } = useQuery({
    queryKey: queryKeys.meta,
    queryFn: () => apiGet<ServerMeta>('/api/v1/meta'),
  });

  if (isPending) {
    return <Skeleton className="h-5 w-56" />;
  }

  if (error) {
    const message =
      error instanceof ApiRequestError ? error.message : 'Could not reach the API.';
    return (
      <p className="flex items-center gap-2 text-sm text-destructive">
        <AlertCircle className="size-4 shrink-0" />
        <span>{message}</span>
      </p>
    );
  }

  return (
    <p className="flex items-center gap-2 text-sm text-muted-foreground">
      <CheckCircle2 className="size-4 shrink-0 text-[var(--success)]" />
      <span>
        API reachable — {data.name} {data.build.version}{' '}
        <span className="text-xs">({data.environment})</span>
      </span>
    </p>
  );
}
