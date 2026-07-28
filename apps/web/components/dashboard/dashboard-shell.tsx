'use client';

import { TriangleAlert } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useEffect } from 'react';

import { AppHeader } from '@/components/app-header';
import { AuthCallbackBanner } from '@/components/auth/auth-callback-banner';
import { AccountsGrid, AccountsGridSkeleton } from '@/components/dashboard/accounts-grid';
import { StorageGraph } from '@/components/dashboard/storage-graph';
import { SummaryCards, SummaryCardsSkeleton } from '@/components/dashboard/summary-cards';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useAccounts } from '@/lib/accounts';
import { useSession } from '@/lib/auth';

/**
 * The dashboard, gated on a live session.
 *
 * Gating happens in the browser because the session cookie is scoped to the API
 * origin — a Next.js server component cannot see it, and proxying the check
 * through the Next server would put user data somewhere this app keeps clean.
 */
export function DashboardShell() {
  const { user, isPending, error } = useSession();
  const router = useRouter();

  const signedOut = !isPending && !error && user === null;

  useEffect(() => {
    if (signedOut) router.replace('/');
  }, [signedOut, router]);

  if (error) {
    return (
      <div className="mx-auto max-w-2xl px-4 py-16 sm:px-6">
        <Alert variant="destructive">
          <TriangleAlert />
          <div className="min-w-0">
            <AlertTitle>Could not reach the SangamDrive API</AlertTitle>
            <AlertDescription>{error.message}</AlertDescription>
          </div>
        </Alert>
      </div>
    );
  }

  if (isPending || signedOut || !user) {
    return <ShellSkeleton />;
  }

  return (
    <div className="min-h-dvh">
      <AppHeader user={user} />

      <main className="mx-auto max-w-7xl space-y-6 px-4 py-8 sm:px-6">
        {/* useSearchParams needs a boundary of its own */}
        <Suspense fallback={null}>
          <AuthCallbackBanner />
        </Suspense>

        <Overview />
      </main>
    </div>
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
    return (
      <div className="space-y-6">
        <SummaryCardsSkeleton />
        <AccountsGridSkeleton />
      </div>
    );
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

function ShellSkeleton() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-4 py-10 sm:px-6">
      <Skeleton className="h-8 w-40" />
      <Skeleton className="h-4 w-72" />
      <SummaryCardsSkeleton />
      <AccountsGridSkeleton />
    </div>
  );
}
