'use client';

import { TriangleAlert } from 'lucide-react';
import { Suspense } from 'react';

import { AppShell } from '@/components/app-shell';
import { AuthCallbackBanner } from '@/components/auth/auth-callback-banner';
import { AccountsGrid, AccountsGridSkeleton } from '@/components/dashboard/accounts-grid';
import { StorageGraph } from '@/components/dashboard/storage-graph';
import { SummaryCards, SummaryCardsSkeleton } from '@/components/dashboard/summary-cards';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useAccounts } from '@/lib/accounts';

export function DashboardShell() {
  return (
    <AppShell fallback={<OverviewSkeleton withHeading />}>
      {/* useSearchParams needs a boundary of its own */}
      <Suspense fallback={null}>
        <AuthCallbackBanner />
      </Suspense>

      <Overview />
    </AppShell>
  );
}

/** Storage totals, the combined graph, and the per-Drive cards. */
function Overview() {
  const { accounts, failures, summary, isPending, error } = useAccounts();

  if (error) {
    return (
      <Alert variant="destructive">
        <TriangleAlert />
        <div className="min-w-0">
          <AlertTitle>Could not load your connected accounts</AlertTitle>
          <AlertDescription>{error.message}</AlertDescription>
        </div>
      </Alert>
    );
  }

  if (isPending) {
    return <OverviewSkeleton />;
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Storage</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Live figures from Google Drive. Nothing here is stored by SangamDrive.
        </p>
      </div>

      <SummaryCards summary={summary} failureCount={failures.size} />

      <StorageGraph accounts={accounts} totalLimit={summary.total_limit} />

      <div>
        <h2 className="mb-4 text-lg font-semibold tracking-tight">
          Connected accounts
          <span className="ml-2 text-sm font-normal text-muted-foreground">
            {accounts.length}
          </span>
        </h2>
        <AccountsGrid accounts={accounts} failures={failures} />
      </div>
    </div>
  );
}

function OverviewSkeleton({ withHeading = false }: { withHeading?: boolean }) {
  return (
    <div className="space-y-6">
      {withHeading && (
        <>
          <Skeleton className="h-8 w-40" />
          <Skeleton className="h-4 w-72" />
        </>
      )}
      <SummaryCardsSkeleton />
      <AccountsGridSkeleton />
    </div>
  );
}
