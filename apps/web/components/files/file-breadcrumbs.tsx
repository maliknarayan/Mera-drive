'use client';

import type { Breadcrumb } from '@sangamdrive/shared';
import { ChevronRight, HardDrive, Layers } from 'lucide-react';

import { cn } from '@/lib/utils';

interface FileBreadcrumbsProps {
  /** Root-first ancestors of the open folder. Empty at a root. */
  path: Breadcrumb[];
  /** Email of the Drive being browsed, or undefined for the merged view. */
  accountEmail?: string;
  onOpenAllDrives: () => void;
  onOpenAccountRoot: () => void;
  onOpenFolder: (folderId: string) => void;
}

/**
 * The trail from "All Drives" down to the open folder.
 *
 * The merged view has no single path, so the account crumb is where a trail
 * begins — everything above it is the fan-out.
 */
export function FileBreadcrumbs({
  path,
  accountEmail,
  onOpenAllDrives,
  onOpenAccountRoot,
  onOpenFolder,
}: FileBreadcrumbsProps) {
  const atMergedRoot = !accountEmail;

  return (
    <nav aria-label="Folder path" className="flex min-w-0 items-center gap-0.5 text-sm">
      <Crumb icon={Layers} current={atMergedRoot} onClick={onOpenAllDrives}>
        All Drives
      </Crumb>

      {accountEmail && (
        <>
          <Separator />
          <Crumb icon={HardDrive} current={path.length === 0} onClick={onOpenAccountRoot}>
            {accountEmail}
          </Crumb>
        </>
      )}

      {path.map((crumb, index) => (
        <span key={crumb.id} className="flex min-w-0 items-center gap-0.5">
          <Separator />
          <Crumb
            current={index === path.length - 1}
            onClick={() => onOpenFolder(crumb.id)}
          >
            {crumb.name}
          </Crumb>
        </span>
      ))}
    </nav>
  );
}

interface CrumbProps {
  children: React.ReactNode;
  current: boolean;
  onClick: () => void;
  icon?: React.ComponentType<{ className?: string }>;
}

function Crumb({ children, current, onClick, icon: Icon }: CrumbProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={current ? 'page' : undefined}
      disabled={current}
      className={cn(
        'flex min-w-0 items-center gap-1.5 rounded-md px-2 py-1 transition-colors',
        current
          ? 'font-medium text-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {Icon && <Icon className="size-3.5 shrink-0" />}
      <span className="truncate">{children}</span>
    </button>
  );
}

function Separator() {
  return <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/60" aria-hidden />;
}
