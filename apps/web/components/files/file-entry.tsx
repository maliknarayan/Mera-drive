'use client';

import {
  type DriveFile,
  FILE_KIND_LABELS,
  type ListScope,
  type ViewMode,
} from '@sangamdrive/shared';
import {
  ExternalLink,
  Loader2,
  Pencil,
  RotateCcw,
  Star,
  Trash2,
  Undo2,
  X,
} from 'lucide-react';
import { useState } from 'react';

import { FileIcon } from '@/components/files/file-icon';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useDeleteFile, useUpdateFile } from '@/lib/files';
import { cn, formatBytes, formatFileDate } from '@/lib/utils';

interface FileEntryProps {
  file: DriveFile;
  scope: ListScope;
  view: ViewMode;
  /** Whether the Drive badge is worth showing — false when one Drive is selected. */
  showDrive: boolean;
  onOpenFolder: (file: DriveFile) => void;
}

/**
 * One file, as a list row or a grid tile.
 *
 * Rename and delete confirmation live here rather than in a dialog: the browser
 * has no dialog primitive yet, and an inline edit keeps the row in context.
 */
export function FileEntry({ file, scope, view, showDrive, onOpenFolder }: FileEntryProps) {
  const update = useUpdateFile();
  const remove = useDeleteFile();

  const [renaming, setRenaming] = useState(false);
  const [draftName, setDraftName] = useState(file.name);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const busy = update.isPending || remove.isPending;
  const inTrash = scope === 'trash';
  const canOpen = file.kind === 'folder' && !inTrash;
  const error = update.error ?? remove.error;

  function submitRename() {
    const next = draftName.trim();
    if (!next || next === file.name) {
      setRenaming(false);
      setDraftName(file.name);
      return;
    }
    update.mutate(
      { accountId: file.account_id, fileId: file.id, name: next },
      { onSuccess: () => setRenaming(false) },
    );
  }

  const name = renaming ? (
    <form
      className="flex items-center gap-1.5"
      onSubmit={(event) => {
        event.preventDefault();
        submitRename();
      }}
    >
      <Input
        autoFocus
        value={draftName}
        onChange={(event) => setDraftName(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            setRenaming(false);
            setDraftName(file.name);
          }
        }}
        aria-label={`Rename ${file.name}`}
        className="h-7 text-sm"
        disabled={busy}
      />
      <Button type="submit" size="sm" className="h-7" disabled={busy}>
        {update.isPending && <Loader2 className="animate-spin" />}
        Save
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className="size-7"
        aria-label="Cancel rename"
        disabled={busy}
        onClick={() => {
          setRenaming(false);
          setDraftName(file.name);
        }}
      >
        <X />
      </Button>
    </form>
  ) : canOpen ? (
    <button
      type="button"
      onClick={() => onOpenFolder(file)}
      className="truncate text-left text-sm font-medium hover:underline"
    >
      {file.name}
    </button>
  ) : (
    <span className="truncate text-sm font-medium" title={file.name}>
      {file.name}
    </span>
  );

  const actions = (
    <div className="flex items-center gap-0.5">
      {inTrash ? (
        <>
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            aria-label={`Restore ${file.name}`}
            title="Restore"
            disabled={busy || !file.capabilities.can_trash}
            onClick={() =>
              update.mutate({ accountId: file.account_id, fileId: file.id, trashed: false })
            }
          >
            <Undo2 />
          </Button>

          {confirmingDelete ? (
            <ConfirmDelete
              label="Delete forever?"
              pending={remove.isPending}
              onConfirm={() =>
                remove.mutate({
                  accountId: file.account_id,
                  fileId: file.id,
                  permanent: true,
                })
              }
              onCancel={() => setConfirmingDelete(false)}
            />
          ) : (
            <Button
              variant="ghost"
              size="icon"
              className="size-8 text-destructive"
              aria-label={`Delete ${file.name} forever`}
              title="Delete forever"
              disabled={busy || !file.capabilities.can_delete}
              onClick={() => setConfirmingDelete(true)}
            >
              <RotateCcw className="rotate-90" />
            </Button>
          )}
        </>
      ) : (
        <>
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            aria-label={file.starred ? `Unstar ${file.name}` : `Star ${file.name}`}
            title={file.starred ? 'Unstar' : 'Star'}
            disabled={busy}
            onClick={() =>
              update.mutate({
                accountId: file.account_id,
                fileId: file.id,
                starred: !file.starred,
              })
            }
          >
            <Star className={cn(file.starred && 'fill-[var(--warning)] text-[var(--warning)]')} />
          </Button>

          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            aria-label={`Rename ${file.name}`}
            title="Rename"
            disabled={busy || renaming || !file.capabilities.can_rename}
            onClick={() => setRenaming(true)}
          >
            <Pencil />
          </Button>

          {confirmingDelete ? (
            <ConfirmDelete
              label="Move to trash?"
              pending={remove.isPending}
              onConfirm={() =>
                remove.mutate({ accountId: file.account_id, fileId: file.id })
              }
              onCancel={() => setConfirmingDelete(false)}
            />
          ) : (
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label={`Move ${file.name} to trash`}
              title="Move to trash"
              disabled={busy || !file.capabilities.can_trash}
              onClick={() => setConfirmingDelete(true)}
            >
              <Trash2 />
            </Button>
          )}
        </>
      )}

      <a
        href={file.web_view_link}
        target="_blank"
        rel="noreferrer noopener"
        aria-label={`Open ${file.name} in Google Drive`}
        title="Open in Drive"
        className="grid size-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        <ExternalLink className="size-4" />
      </a>
    </div>
  );

  const meta = (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
      <span>{FILE_KIND_LABELS[file.kind]}</span>
      <span aria-hidden>·</span>
      <span className="tabular-nums">{formatFileDate(file.modified_at)}</span>
      <span aria-hidden>·</span>
      <span className="tabular-nums">{file.size === null ? '—' : formatBytes(file.size)}</span>
      {file.shared && <Badge variant="outline">Shared</Badge>}
      {showDrive && (
        <Badge variant="outline" className="max-w-[14rem] truncate">
          {file.account_email}
        </Badge>
      )}
    </div>
  );

  const shell = cn(
    'rounded-lg border bg-card transition-colors hover:bg-accent/40',
    busy && 'opacity-60',
  );

  if (view === 'grid') {
    return (
      <li className={shell}>
        <div className="flex h-full flex-col gap-2 p-3">
          <FileIcon kind={file.kind} className="size-8" />
          <div className="min-w-0">{name}</div>
          {meta}
          {error && <p className="text-xs text-destructive">{error.message}</p>}
          <div className="mt-auto border-t pt-2">{actions}</div>
        </div>
      </li>
    );
  }

  return (
    <li className={shell}>
      <div className="flex items-center gap-3 px-3 py-2.5">
        <FileIcon kind={file.kind} className="size-5" />

        <div className="min-w-0 flex-1">
          {name}
          <div className="mt-0.5">{meta}</div>
          {error && <p className="mt-1 text-xs text-destructive">{error.message}</p>}
        </div>

        {actions}
      </div>
    </li>
  );
}

interface ConfirmDeleteProps {
  label: string;
  pending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

function ConfirmDelete({ label, pending, onConfirm, onCancel }: ConfirmDeleteProps) {
  return (
    <span className="flex items-center gap-1.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <Button variant="destructive" size="sm" className="h-7" disabled={pending} onClick={onConfirm}>
        {pending && <Loader2 className="animate-spin" />}
        Confirm
      </Button>
      <Button variant="ghost" size="sm" className="h-7" disabled={pending} onClick={onCancel}>
        Cancel
      </Button>
    </span>
  );
}
