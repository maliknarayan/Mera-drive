'use client';

import type {
  DriveFile,
  ListScope,
  SortDirection,
  SortField,
  ViewMode,
} from '@sangamdrive/shared';
import { FolderOpen, Loader2, TriangleAlert } from 'lucide-react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useCallback, useMemo } from 'react';

import { FileBreadcrumbs } from '@/components/files/file-breadcrumbs';
import { FileEntry } from '@/components/files/file-entry';
import { FilesToolbar } from '@/components/files/files-toolbar';
import { NewFolder } from '@/components/files/new-folder';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useAccounts } from '@/lib/accounts';
import { useFiles } from '@/lib/files';
import { cn } from '@/lib/utils';

const SCOPES: readonly ListScope[] = ['children', 'starred', 'recent', 'trash'];
const SORT_FIELDS: readonly SortField[] = ['name', 'modified_at', 'size', 'account_email'];

const SCOPE_HEADINGS: Record<ListScope, string> = {
  children: 'Files',
  starred: 'Starred',
  recent: 'Recent',
  trash: 'Trash',
};

const EMPTY_MESSAGES: Record<ListScope, string> = {
  children: 'This folder is empty.',
  starred: 'Nothing starred yet.',
  recent: 'No recent activity.',
  trash: 'The trash is empty.',
};

/**
 * The unified file browser.
 *
 * Listing state lives in the URL so back, forward and refresh all behave, and a
 * folder is a link someone can send to themselves. Nothing is stored server-side.
 */
export function FileBrowser() {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();

  const accountId = params.get('account') || undefined;
  const parentId = params.get('parent') || undefined;
  const scope = pick(params.get('scope'), SCOPES, 'children');
  const sort = pick(params.get('sort'), SORT_FIELDS, 'name');
  const direction: SortDirection = params.get('direction') === 'desc' ? 'desc' : 'asc';
  const view: ViewMode = params.get('view') === 'grid' ? 'grid' : 'list';

  const navigate = useCallback(
    (changes: Record<string, string | undefined>) => {
      const next = new URLSearchParams(params.toString());
      for (const [key, value] of Object.entries(changes)) {
        if (value) next.set(key, value);
        else next.delete(key);
      }
      const query = next.toString();
      router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
    },
    [params, pathname, router],
  );

  const { accounts, isPending: accountsPending } = useAccounts();
  const files = useFiles({ accountId, parentId, scope, sort, direction });

  const accountEmail = useMemo(
    () => accounts.find((account) => account.id === accountId)?.email,
    [accounts, accountId],
  );

  // a folder id only means something inside its own Drive, so both travel together
  const openFolder = useCallback(
    (file: DriveFile) => navigate({ account: file.account_id, parent: file.id, scope: 'children' }),
    [navigate],
  );

  const failures = [...files.failures.values()];

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{SCOPE_HEADINGS[scope]}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Live from Google Drive on every request. SangamDrive stores no file metadata.
          </p>
        </div>
        {scope === 'children' && <NewFolder accountId={accountId} parentId={parentId} />}
      </div>

      <FilesToolbar
        accounts={accounts}
        accountId={accountId}
        scope={scope}
        sort={sort}
        direction={direction}
        view={view}
        isFetching={files.isFetching}
        onSelectAccount={(id) => navigate({ account: id, parent: undefined })}
        // a parent belongs to one folder listing; the other scopes span the Drive
        onSelectScope={(next) => navigate({ scope: next, parent: undefined })}
        onSelectSort={(next) => navigate({ sort: next })}
        onToggleDirection={() => navigate({ direction: direction === 'asc' ? 'desc' : 'asc' })}
        onSelectView={(next) => navigate({ view: next })}
        onRefresh={files.refetch}
      />

      {scope === 'children' && (
        <FileBreadcrumbs
          path={files.path}
          accountEmail={accountEmail}
          onOpenAllDrives={() => navigate({ account: undefined, parent: undefined })}
          onOpenAccountRoot={() => navigate({ parent: undefined })}
          onOpenFolder={(folderId) => navigate({ parent: folderId })}
        />
      )}

      {failures.length > 0 && (
        <Alert variant="warning">
          <TriangleAlert />
          <div className="min-w-0">
            <AlertTitle>
              {failures.length === 1
                ? 'One Drive could not be listed'
                : `${failures.length} Drives could not be listed`}
            </AlertTitle>
            <ul className="space-y-1 text-muted-foreground">
              {failures.map((failure) => (
                <li key={failure.account_id}>
                  <span className="font-medium">
                    {accounts.find((a) => a.id === failure.account_id)?.email ??
                      failure.account_id}
                  </span>
                  {' — '}
                  {failure.message}
                </li>
              ))}
            </ul>
          </div>
        </Alert>
      )}

      {files.error ? (
        <Alert variant="destructive">
          <TriangleAlert />
          <div className="min-w-0">
            <AlertTitle>Could not load this folder</AlertTitle>
            <AlertDescription>{files.error.message}</AlertDescription>
          </div>
        </Alert>
      ) : files.isPending || accountsPending ? (
        <FileListSkeleton view={view} />
      ) : files.files.length === 0 ? (
        <EmptyState message={EMPTY_MESSAGES[scope]} />
      ) : (
        <>
          <ul
            className={cn(
              view === 'grid'
                ? 'grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'
                : 'space-y-1.5',
            )}
          >
            {files.files.map((file) => (
              <FileEntry
                // the same file id can appear once per Drive, so key on both
                key={`${file.account_id}:${file.id}`}
                file={file}
                scope={scope}
                view={view}
                showDrive={!accountId}
                onOpenFolder={openFolder}
              />
            ))}
          </ul>

          <div className="flex items-center justify-between gap-3 pt-1">
            <p className="text-xs text-muted-foreground">
              {files.files.length} {files.files.length === 1 ? 'item' : 'items'}
              {files.hasNextPage && ' loaded so far'}
            </p>

            {files.hasNextPage && (
              <Button
                variant="outline"
                size="sm"
                onClick={files.fetchNextPage}
                disabled={files.isFetchingNextPage}
              >
                {files.isFetchingNextPage && <Loader2 className="animate-spin" />}
                Load more
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function EmptyState({ message }: { message: string }) {
  return (
    <div className="grid place-items-center gap-2 rounded-xl border border-dashed py-16 text-center">
      <FolderOpen className="size-8 text-muted-foreground" aria-hidden />
      <p className="text-sm text-muted-foreground">{message}</p>
    </div>
  );
}

export function FileListSkeleton({ view = 'list' }: { view?: ViewMode }) {
  return (
    <div
      className={cn(
        view === 'grid'
          ? 'grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'
          : 'space-y-1.5',
      )}
    >
      {Array.from({ length: 8 }, (_, index) => (
        <Skeleton key={index} className={view === 'grid' ? 'h-36' : 'h-14'} />
      ))}
    </div>
  );
}

/** Narrow a query parameter to a known value, falling back to the default. */
function pick<T extends string>(raw: string | null, allowed: readonly T[], fallback: T): T {
  return allowed.includes(raw as T) ? (raw as T) : fallback;
}
