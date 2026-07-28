'use client';

import { type ApiError, type ConnectedAccount, driveAccent } from '@sangamdrive/shared';
import { HardDrive } from 'lucide-react';

import { AccountCard } from '@/components/dashboard/account-card';
import { buttonVariants } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { useReorderAccounts } from '@/lib/accounts';
import { googleAuthUrl } from '@/lib/auth';
import { cn } from '@/lib/utils';

interface AccountsGridProps {
  accounts: ConnectedAccount[];
  failures: Map<string, ApiError>;
}

export function AccountsGrid({ accounts, failures }: AccountsGridProps) {
  const reorder = useReorderAccounts();

  if (accounts.length === 0) {
    return <EmptyState />;
  }

  const move = (from: number, to: number) => {
    const order = accounts.map((account) => account.id);
    const [moved] = order.splice(from, 1);
    if (!moved) return;
    order.splice(to, 0, moved);
    reorder.mutate(order);
  };

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {accounts.map((account, index) => (
          <AccountCard
            key={account.id}
            account={account}
            accent={driveAccent(index)}
            failure={failures.get(account.id)}
            onMoveEarlier={
              index > 0 && !reorder.isPending ? () => move(index, index - 1) : undefined
            }
            onMoveLater={
              index < accounts.length - 1 && !reorder.isPending
                ? () => move(index, index + 1)
                : undefined
            }
          />
        ))}
      </div>

      {reorder.error && <p className="text-sm text-destructive">{reorder.error.message}</p>}
    </div>
  );
}

function EmptyState() {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-4 p-10 text-center">
        <span className="grid size-11 place-items-center rounded-full bg-secondary">
          <HardDrive className="size-5" />
        </span>
        <div>
          <p className="font-medium">No Google accounts connected</p>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            Connect a Google account to see its storage here. Add as many as you like — SangamDrive
            shows them as one.
          </p>
        </div>
        <a
          href={googleAuthUrl({ intent: 'link', scope: 'drive.file', next: '/dashboard' })}
          className={cn(buttonVariants())}
        >
          Connect a Google account
        </a>
      </CardContent>
    </Card>
  );
}

/** Loading state that reserves card-sized space to avoid layout shift. */
export function AccountsGridSkeleton() {
  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      {[0, 1, 2].map((i) => (
        <Card key={i}>
          <CardContent className="space-y-4 p-5">
            <div className="flex items-center gap-3">
              <Skeleton className="size-9 rounded-full" />
              <div className="flex-1 space-y-1.5">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-3 w-44" />
              </div>
            </div>
            <Skeleton className="h-1.5 w-full" />
            <div className="grid grid-cols-3 gap-2">
              <Skeleton className="h-8" />
              <Skeleton className="h-8" />
              <Skeleton className="h-8" />
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
