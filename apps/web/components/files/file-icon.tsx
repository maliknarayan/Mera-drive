import { type FileKind, FILE_KIND_LABELS } from '@sangamdrive/shared';
import {
  File as FileGeneric,
  FileArchive,
  FileAudio,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileType,
  FileVideo,
  Folder,
  Presentation,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

import { cn } from '@/lib/utils';

const ICONS: Record<FileKind, LucideIcon> = {
  folder: Folder,
  image: FileImage,
  video: FileVideo,
  audio: FileAudio,
  pdf: FileType,
  text: FileText,
  archive: FileArchive,
  gdoc: FileText,
  gsheet: FileSpreadsheet,
  gslide: Presentation,
  other: FileGeneric,
};

// folders lead visually as well as in sort order, so they get the accent colour
const TINTS: Partial<Record<FileKind, string>> = {
  folder: 'text-primary',
  image: 'text-[var(--drive-3)]',
  video: 'text-[var(--drive-4)]',
  audio: 'text-[var(--drive-5)]',
  pdf: 'text-destructive',
  gdoc: 'text-[var(--drive-1)]',
  gsheet: 'text-[var(--success)]',
  gslide: 'text-[var(--warning)]',
};

interface FileIconProps {
  kind: FileKind;
  className?: string;
}

export function FileIcon({ kind, className }: FileIconProps) {
  const Icon = ICONS[kind];

  return (
    <Icon
      className={cn('shrink-0', TINTS[kind] ?? 'text-muted-foreground', className)}
      aria-label={FILE_KIND_LABELS[kind]}
    />
  );
}
