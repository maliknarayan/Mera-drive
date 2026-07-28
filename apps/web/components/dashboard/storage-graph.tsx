'use client';

import { type ConnectedAccount, driveAccent } from '@sangamdrive/shared';

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { formatBytes } from '@/lib/utils';

interface StorageGraphProps {
  accounts: ConnectedAccount[];
  totalLimit: number | null;
}

/**
 * One bar showing how combined capacity is spent, segmented per Drive.
 *
 * Deliberately hand-rolled SVG-free markup rather than a charting library: a
 * stacked bar is a few divs, and a self-hosted app should not ship 100 kB of
 * JavaScript to draw one.
 */
export function StorageGraph({ accounts, totalLimit }: StorageGraphProps) {
  const segments = accounts
    .map((account, index) => ({
      id: account.id,
      email: account.email,
      usage: account.quota?.usage ?? 0,
      accent: driveAccent(index),
      unlimited: account.quota?.limit === null && account.quota !== undefined,
    }))
    .filter((segment) => segment.usage > 0);

  if (segments.length === 0) {
    return null;
  }

  const usedTotal = segments.reduce((sum, segment) => sum + segment.usage, 0);
  // scale against capacity when known, otherwise against what is actually used
  const denominator = totalLimit && totalLimit > usedTotal ? totalLimit : usedTotal;

  return (
    <Card>
      <CardHeader className="pb-4">
        <CardTitle className="text-base">Storage across Drives</CardTitle>
        <CardDescription>
          {totalLimit === null
            ? 'Relative usage — no combined cap is reported'
            : `${formatBytes(usedTotal)} of ${formatBytes(totalLimit)} used`}
        </CardDescription>
      </CardHeader>

      <CardContent>
        <div
          className="flex h-3 w-full overflow-hidden rounded-full bg-muted"
          role="img"
          aria-label={`Combined storage usage: ${formatBytes(usedTotal)}${
            totalLimit === null ? '' : ` of ${formatBytes(totalLimit)}`
          }`}
        >
          {segments.map((segment) => (
            <div
              key={segment.id}
              title={`${segment.email} — ${formatBytes(segment.usage)}`}
              style={{
                width: `${(segment.usage / denominator) * 100}%`,
                backgroundColor: segment.accent,
              }}
              className="h-full first:rounded-l-full last:rounded-r-full"
            />
          ))}
        </div>

        <ul className="mt-4 grid gap-x-6 gap-y-2 sm:grid-cols-2">
          {segments.map((segment) => (
            <li key={segment.id} className="flex min-w-0 items-center gap-2 text-sm">
              <span
                className="size-2.5 shrink-0 rounded-full"
                style={{ backgroundColor: segment.accent }}
                aria-hidden
              />
              <span className="truncate text-muted-foreground">{segment.email}</span>
              <span className="ml-auto shrink-0 tabular-nums">{formatBytes(segment.usage)}</span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}
