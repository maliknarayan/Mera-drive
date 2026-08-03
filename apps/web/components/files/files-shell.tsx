'use client';

import { Suspense } from 'react';

import { AppShell } from '@/components/app-shell';
import { FileBrowser, FileListSkeleton } from '@/components/files/file-browser';
import { Skeleton } from '@/components/ui/skeleton';

export function FilesShell() {
  return (
    <AppShell fallback={<BrowserSkeleton />}>
      {/* the browser keeps its state in the URL, so useSearchParams needs a boundary */}
      <Suspense fallback={<BrowserSkeleton />}>
        <FileBrowser />
      </Suspense>
    </AppShell>
  );
}

function BrowserSkeleton() {
  return (
    <div className="space-y-5">
      <Skeleton className="h-8 w-32" />
      <Skeleton className="h-4 w-80" />
      <Skeleton className="h-9 w-full" />
      <FileListSkeleton />
    </div>
  );
}
