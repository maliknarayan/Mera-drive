'use client';

import type {
  ConnectedAccount,
  ListScope,
  SortDirection,
  SortField,
  ViewMode,
} from '@sangamdrive/shared';
import {
  ArrowDownAZ,
  ArrowUpAZ,
  Clock,
  FolderOpen,
  LayoutGrid,
  List,
  RefreshCw,
  Star,
  Trash2,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

const SCOPES: { value: ListScope; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
  { value: 'children', label: 'Files', icon: FolderOpen },
  { value: 'starred', label: 'Starred', icon: Star },
  { value: 'recent', label: 'Recent', icon: Clock },
  { value: 'trash', label: 'Trash', icon: Trash2 },
];

const SORT_FIELDS: { value: SortField; label: string }[] = [
  { value: 'name', label: 'Name' },
  { value: 'modified_at', label: 'Modified' },
  { value: 'size', label: 'Size' },
  { value: 'account_email', label: 'Drive' },
];

interface FilesToolbarProps {
  accounts: ConnectedAccount[];
  accountId?: string;
  scope: ListScope;
  sort: SortField;
  direction: SortDirection;
  view: ViewMode;
  isFetching: boolean;
  onSelectAccount: (accountId: string | undefined) => void;
  onSelectScope: (scope: ListScope) => void;
  onSelectSort: (sort: SortField) => void;
  onToggleDirection: () => void;
  onSelectView: (view: ViewMode) => void;
  onRefresh: () => void;
}

export function FilesToolbar({
  accounts,
  accountId,
  scope,
  sort,
  direction,
  view,
  isFetching,
  onSelectAccount,
  onSelectScope,
  onSelectSort,
  onToggleDirection,
  onSelectView,
  onRefresh,
}: FilesToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Segmented label="View">
        {SCOPES.map(({ value, label, icon: Icon }) => (
          <SegmentedButton
            key={value}
            active={scope === value}
            onClick={() => onSelectScope(value)}
          >
            <Icon className="size-3.5" />
            <span className="hidden sm:inline">{label}</span>
          </SegmentedButton>
        ))}
      </Segmented>

      <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <span className="sr-only sm:not-sr-only">Drive</span>
        <select
          value={accountId ?? ''}
          onChange={(event) => onSelectAccount(event.target.value || undefined)}
          className="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <option value="">All Drives</option>
          {accounts.map((account) => (
            <option key={account.id} value={account.id}>
              {account.email}
            </option>
          ))}
        </select>
      </label>

      <div className="ml-auto flex items-center gap-2">
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <span className="sr-only sm:not-sr-only">Sort</span>
          <select
            value={sort}
            onChange={(event) => onSelectSort(event.target.value as SortField)}
            className="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {SORT_FIELDS.map(({ value, label }) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>

        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={onToggleDirection}
          aria-label={direction === 'asc' ? 'Sort descending' : 'Sort ascending'}
          title={direction === 'asc' ? 'Ascending' : 'Descending'}
        >
          {direction === 'asc' ? <ArrowDownAZ /> : <ArrowUpAZ />}
        </Button>

        <Segmented label="Layout">
          <SegmentedButton active={view === 'list'} onClick={() => onSelectView('list')}>
            <List className="size-3.5" />
            <span className="sr-only">List</span>
          </SegmentedButton>
          <SegmentedButton active={view === 'grid'} onClick={() => onSelectView('grid')}>
            <LayoutGrid className="size-3.5" />
            <span className="sr-only">Grid</span>
          </SegmentedButton>
        </Segmented>

        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={onRefresh}
          disabled={isFetching}
          aria-label="Refresh listing"
          title="Refresh"
        >
          <RefreshCw className={cn(isFetching && 'animate-spin')} />
        </Button>
      </div>
    </div>
  );
}

function Segmented({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div role="group" aria-label={label} className="flex items-center gap-0.5 rounded-md border p-0.5">
      {children}
    </div>
  );
}

interface SegmentedButtonProps {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}

function SegmentedButton({ active, onClick, children }: SegmentedButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        'flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium transition-colors',
        active
          ? 'bg-secondary text-secondary-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {children}
    </button>
  );
}
