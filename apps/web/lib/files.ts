'use client';

import type {
  ApiError,
  Breadcrumb,
  DriveFile,
  FileListMeta,
  ListScope,
  SortDirection,
  SortField,
} from '@sangamdrive/shared';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';

import { ApiRequestError, apiRequest } from '@/lib/api';
import { queryKeys } from '@/lib/query-client';

/** Everything that identifies one listing. Mirrors the API's query parameters. */
export interface FilesQuery {
  /** Undefined fans out across every connected Drive. */
  accountId?: string;
  /** Folder to open. Only meaningful with accountId — folder ids are Drive-scoped. */
  parentId?: string;
  scope: ListScope;
  sort: SortField;
  direction: SortDirection;
}

export interface FilesState {
  files: DriveFile[];
  /** Breadcrumbs for a folder listing, root-first. Empty for a merged listing. */
  path: Breadcrumb[];
  /** Per-account failures from the fan-out, keyed by account id. */
  failures: Map<string, ApiError>;
  isPending: boolean;
  isFetching: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  error: ApiRequestError | null;
  refetch: () => void;
}

type FilesResult = { data: DriveFile[]; meta?: FileListMeta };

/**
 * One listing, paged.
 *
 * The cursor bundles a Google page token per account, so "load more" only calls
 * the Drives that still have pages left. Ordering is per page rather than global:
 * Google paginates per account and offers no cross-account cursor.
 */
export function useFiles(query: FilesQuery): FilesState {
  const infinite = useInfiniteQuery({
    queryKey: queryKeys.files({ ...query }),
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      apiRequest<DriveFile[]>('/api/v1/files', {
        query: {
          account_id: query.accountId,
          parent: query.parentId,
          scope: query.scope,
          sort: query.sort,
          direction: query.direction,
          page: pageParam || undefined,
        },
      }) as Promise<FilesResult>,
    getNextPageParam: (last: FilesResult) => last.meta?.next_page_token || undefined,
  });

  // stable identities so consumers only re-render on a new response
  const files = useMemo(
    () => (infinite.data?.pages ?? []).flatMap((page) => page.data ?? []),
    [infinite.data],
  );

  const path = useMemo(() => infinite.data?.pages[0]?.meta?.path ?? [], [infinite.data]);

  const failures = useMemo(() => {
    const map = new Map<string, ApiError>();
    for (const page of infinite.data?.pages ?? []) {
      for (const failure of page.meta?.errors ?? []) {
        // last page wins: a Drive that recovered should not stay flagged
        if (failure.account_id) map.set(failure.account_id, failure);
      }
    }
    return map;
  }, [infinite.data]);

  return {
    files,
    path,
    failures,
    isPending: infinite.isPending,
    isFetching: infinite.isFetching,
    hasNextPage: infinite.hasNextPage,
    isFetchingNextPage: infinite.isFetchingNextPage,
    fetchNextPage: () => void infinite.fetchNextPage(),
    error: infinite.error instanceof ApiRequestError ? infinite.error : null,
    refetch: () => void infinite.refetch(),
  };
}

/**
 * Invalidate every listing.
 *
 * A mutation in one folder can change what another listing shows — trashing a
 * file removes it from `children` and adds it to `trash` — and the client holds
 * the only cache, so the whole prefix goes.
 */
function useInvalidateFiles() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: ['files'] });
}

export interface CreateFolderInput {
  accountId: string;
  name: string;
  /** Omit to create in that Drive's root. */
  parentId?: string;
}

export function useCreateFolder() {
  const invalidate = useInvalidateFiles();

  return useMutation({
    mutationFn: (input: CreateFolderInput) =>
      apiRequest<DriveFile>('/api/v1/files/folder', {
        method: 'POST',
        body: {
          account_id: input.accountId,
          name: input.name,
          parent_id: input.parentId,
        },
      }),
    onSuccess: () => invalidate(),
  });
}

export interface UpdateFileInput {
  accountId: string;
  fileId: string;
  name?: string;
  starred?: boolean;
  trashed?: boolean;
  /** Moves the file into another folder in the same Drive. */
  parentId?: string;
}

export function useUpdateFile() {
  const invalidate = useInvalidateFiles();

  return useMutation({
    mutationFn: ({ accountId, fileId, ...changes }: UpdateFileInput) =>
      apiRequest<DriveFile>(`/api/v1/files/${accountId}/${fileId}`, {
        method: 'PATCH',
        body: {
          name: changes.name,
          starred: changes.starred,
          trashed: changes.trashed,
          parent_id: changes.parentId,
        },
      }),
    onSuccess: () => invalidate(),
  });
}

export interface DeleteFileInput {
  accountId: string;
  fileId: string;
  /** Erases the file instead of trashing it. Google offers no undo. */
  permanent?: boolean;
}

export function useDeleteFile() {
  const invalidate = useInvalidateFiles();

  return useMutation({
    mutationFn: ({ accountId, fileId, permanent }: DeleteFileInput) =>
      apiRequest<void>(`/api/v1/files/${accountId}/${fileId}`, {
        method: 'DELETE',
        query: permanent ? { permanent: true } : undefined,
      }),
    onSuccess: () => invalidate(),
  });
}
