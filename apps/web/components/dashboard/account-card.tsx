'use client';

import {
  ACCOUNT_STATUS_LABELS,
  type ApiError,
  type ConnectedAccount,
} from '@sangamdrive/shared';
import {
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Loader2,
  RefreshCw,
  ShieldPlus,
  Trash2,
  TriangleAlert,
} from 'lucide-react';
import Image from 'next/image';
import { useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button, buttonVariants } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Progress } from '@/components/ui/progress';
import { useDisconnectAccount } from '@/lib/accounts';
import { googleAuthUrl } from '@/lib/auth';
import { cn, formatBytes, usagePercent } from '@/lib/utils';

interface AccountCardProps {
  account: ConnectedAccount;
  accent: string;
  failure?: ApiError;
  /** Undefined when the card is already first or last, or a move is in flight. */
  onMoveEarlier?: () => void;
  onMoveLater?: () => void;
}

export function AccountCard({
  account,
  accent,
  failure,
  onMoveEarlier,
  onMoveLater,
}: AccountCardProps) {
  const disconnect = useDisconnectAccount();
  const [confirmingDisconnect, setConfirmingDisconnect] = useState(false);

  const quota = account.quota;
  const percent = quota ? usagePercent(quota.usage, quota.limit) : null;
  const needsReconnect = account.status === 'reauth_required';
  const canUpgrade = account.scope === 'drive.file' && !needsReconnect;

  return (
    <Card className="overflow-hidden">
      {/* accent stripe ties this card to its segment in the storage graph */}
      <div className="h-1 w-full" style={{ backgroundColor: accent }} aria-hidden />

      <CardContent className="space-y-4 p-5">
        <div className="flex items-start gap-3">
          <Avatar account={account} />

          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">{account.name || account.email}</p>
            <p className="truncate text-xs text-muted-foreground">{account.email}</p>
          </div>

          <StatusBadge account={account} />
        </div>

        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline">
            {account.scope === 'drive' ? 'Full Drive access' : 'App files only'}
          </Badge>
          {quota?.limit === null && <Badge variant="outline">Unlimited storage</Badge>}
        </div>

        {failure ? (
          <p className="flex items-start gap-2 text-xs text-[var(--warning)]">
            <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
            <span>{failure.message}</span>
          </p>
        ) : (
          <UsageBlock quota={quota} percent={percent} accent={accent} email={account.email} />
        )}

        <div className="flex flex-wrap items-center gap-2 border-t pt-4">
          {needsReconnect && (
            <a
              href={googleAuthUrl({
                intent: 'reconnect',
                scope: account.scope,
                accountId: account.id,
                next: '/dashboard',
              })}
              className={cn(buttonVariants({ size: 'sm' }))}
            >
              <RefreshCw />
              Reconnect
            </a>
          )}

          {canUpgrade && (
            <a
              href={googleAuthUrl({
                intent: 'upgrade',
                scope: 'drive',
                accountId: account.id,
                next: '/dashboard',
              })}
              className={cn(buttonVariants({ variant: 'outline', size: 'sm' }))}
            >
              <ShieldPlus />
              Upgrade access
            </a>
          )}

          <a
            href="https://drive.google.com"
            target="_blank"
            rel="noreferrer noopener"
            className={cn(buttonVariants({ variant: 'ghost', size: 'sm' }))}
          >
            <ExternalLink />
            Open in Drive
          </a>

          <div className="ml-auto flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label={`Move ${account.email} earlier`}
              disabled={!onMoveEarlier}
              onClick={onMoveEarlier}
            >
              <ChevronLeft />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label={`Move ${account.email} later`}
              disabled={!onMoveLater}
              onClick={onMoveLater}
            >
              <ChevronRight />
            </Button>

            {confirmingDisconnect ? (
              <div className="flex items-center gap-1.5">
                <span className="text-xs text-muted-foreground">Disconnect?</span>
                <Button
                  variant="destructive"
                  size="sm"
                  disabled={disconnect.isPending}
                  onClick={() => disconnect.mutate(account.id)}
                >
                  {disconnect.isPending && <Loader2 className="animate-spin" />}
                  Confirm
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setConfirmingDisconnect(false)}
                  disabled={disconnect.isPending}
                >
                  Cancel
                </Button>
              </div>
            ) : (
              <Button
                variant="ghost"
                size="sm"
                aria-label={`Disconnect ${account.email}`}
                onClick={() => setConfirmingDisconnect(true)}
              >
                <Trash2 />
                Disconnect
              </Button>
            )}
          </div>
        </div>

        {disconnect.error && (
          <p className="text-xs text-destructive">{disconnect.error.message}</p>
        )}
      </CardContent>
    </Card>
  );
}

function Avatar({ account }: { account: ConnectedAccount }) {
  if (!account.avatar_url) {
    return (
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-secondary text-xs font-medium">
        {account.email.slice(0, 1).toUpperCase()}
      </span>
    );
  }
  return (
    <Image
      src={account.avatar_url}
      alt=""
      width={36}
      height={36}
      className="size-9 shrink-0 rounded-full"
    />
  );
}

function StatusBadge({ account }: { account: ConnectedAccount }) {
  const label = ACCOUNT_STATUS_LABELS[account.status];

  if (account.status === 'connected') {
    return <Badge variant="success">{label}</Badge>;
  }
  if (account.status === 'reauth_required') {
    return <Badge variant="warning">{label}</Badge>;
  }
  return <Badge variant="outline">{label}</Badge>;
}

interface UsageBlockProps {
  quota: ConnectedAccount['quota'];
  percent: number | null;
  accent: string;
  email: string;
}

function UsageBlock({ quota, percent, accent, email }: UsageBlockProps) {
  if (!quota) {
    return <p className="text-xs text-muted-foreground">Storage figures unavailable.</p>;
  }

  return (
    <div>
      <Progress value={percent} label={`Storage used by ${email}`} accent={accent} />
      <dl className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <Figure term="Used" value={formatBytes(quota.usage)} />
        <Figure
          term="Free"
          value={quota.limit === null ? 'Unlimited' : formatBytes(quota.limit - quota.usage)}
        />
        <Figure
          term="Quota"
          value={quota.limit === null ? 'Unlimited' : formatBytes(quota.limit)}
        />
      </dl>
    </div>
  );
}

function Figure({ term, value }: { term: string; value: string }) {
  return (
    <div>
      <dt className="text-muted-foreground">{term}</dt>
      <dd className="mt-0.5 font-medium tabular-nums">{value}</dd>
    </div>
  );
}
