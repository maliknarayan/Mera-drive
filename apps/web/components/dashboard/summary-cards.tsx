'use client';

import type { StorageSummary } from '@sangamdrive/shared';
import { CircleCheck, Database, HardDrive, TriangleAlert } from 'lucide-react';

import { Card, CardContent } from '@/components/ui/card';
import { Progress } from '@/components/ui/progress';
import { Skeleton } from '@/components/ui/skeleton';
import { formatBytes, usagePercent } from '@/lib/utils';

interface SummaryCardsProps {
  summary: StorageSummary;
  failureCount: number;
}

export function SummaryCards({ summary, failureCount }: SummaryCardsProps) {
  const percent = usagePercent(summary.total_usage, summary.total_limit);

  return (
    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Stat
        label="Total storage"
        value={summary.total_limit === null ? 'Unlimited' : formatBytes(summary.total_limit)}
        detail={
          summary.unlimited_count > 0
            ? `${summary.unlimited_count} unlimited ${plural(summary.unlimited_count, 'Drive')} excluded`
            : undefined
        }
        icon={<Database className="size-4" />}
      />

      <Stat
        label="Used"
        value={formatBytes(summary.total_usage)}
        detail={percent === null ? 'across all Drives' : `${percent.toFixed(1)}% of total`}
        icon={<HardDrive className="size-4" />}
      >
        <Progress value={percent} label="Total storage used" className="mt-3" />
      </Stat>

      <Stat
        label="Free"
        value={summary.total_free === null ? 'Unlimited' : formatBytes(summary.total_free)}
        detail={summary.total_free === null ? 'no cap reported' : 'remaining across all Drives'}
        icon={<CircleCheck className="size-4" />}
      />

      <Stat
        label="Drives"
        value={String(summary.account_count)}
        detail={
          failureCount > 0
            ? `${failureCount} need${failureCount === 1 ? 's' : ''} attention`
            : `${summary.connected_count} connected`
        }
        tone={failureCount > 0 ? 'warning' : undefined}
        icon={
          failureCount > 0 ? (
            <TriangleAlert className="size-4 text-[var(--warning)]" />
          ) : (
            <HardDrive className="size-4" />
          )
        }
      />
    </div>
  );
}

interface StatProps {
  label: string;
  value: string;
  detail?: string;
  icon: React.ReactNode;
  tone?: 'warning';
  children?: React.ReactNode;
}

function Stat({ label, value, detail, icon, tone, children }: StatProps) {
  return (
    <Card>
      <CardContent className="p-5">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm text-muted-foreground">{label}</span>
          <span className="text-muted-foreground">{icon}</span>
        </div>
        <p className="mt-2 text-2xl font-semibold tabular-nums tracking-tight">{value}</p>
        {detail && (
          <p
            className={
              tone === 'warning'
                ? 'mt-1 text-xs text-[var(--warning)]'
                : 'mt-1 text-xs text-muted-foreground'
            }
          >
            {detail}
          </p>
        )}
        {children}
      </CardContent>
    </Card>
  );
}

/** Loading state that reserves the same space, so nothing shifts on arrival. */
export function SummaryCardsSkeleton() {
  return (
    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      {[0, 1, 2, 3].map((i) => (
        <Card key={i}>
          <CardContent className="p-5">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="mt-3 h-7 w-28" />
            <Skeleton className="mt-2 h-3 w-32" />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function plural(count: number, word: string): string {
  return count === 1 ? word : `${word}s`;
}
