'use client';

import { FolderPlus, Loader2, X } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useCreateFolder } from '@/lib/files';

interface NewFolderProps {
  /** Undefined in the merged view, where there is no single Drive to create in. */
  accountId?: string;
  parentId?: string;
}

/**
 * Create a folder in the Drive currently being browsed.
 *
 * Disabled in the merged view: a folder has to live in exactly one Drive, and
 * guessing which one would be worse than asking.
 */
export function NewFolder({ accountId, parentId }: NewFolderProps) {
  const create = useCreateFolder();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');

  if (!accountId) {
    return (
      <Button variant="outline" size="sm" disabled title="Pick a single Drive first">
        <FolderPlus />
        New folder
      </Button>
    );
  }

  if (!open) {
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={() => {
          setName('');
          create.reset();
          setOpen(true);
        }}
      >
        <FolderPlus />
        New folder
      </Button>
    );
  }

  return (
    <form
      className="flex items-start gap-1.5"
      onSubmit={(event) => {
        event.preventDefault();
        const trimmed = name.trim();
        if (!trimmed) return;

        create.mutate(
          { accountId, name: trimmed, parentId },
          {
            onSuccess: () => {
              setOpen(false);
              setName('');
            },
          },
        );
      }}
    >
      <div>
        <Input
          autoFocus
          value={name}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Escape') setOpen(false);
          }}
          placeholder="Folder name"
          aria-label="New folder name"
          maxLength={255}
          className="h-8 w-48 text-sm"
          disabled={create.isPending}
        />
        {create.error && (
          <p className="mt-1 max-w-48 text-xs text-destructive">{create.error.message}</p>
        )}
      </div>

      <Button type="submit" size="sm" disabled={create.isPending || !name.trim()}>
        {create.isPending && <Loader2 className="animate-spin" />}
        Create
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className="size-8"
        aria-label="Cancel"
        disabled={create.isPending}
        onClick={() => setOpen(false)}
      >
        <X />
      </Button>
    </form>
  );
}
