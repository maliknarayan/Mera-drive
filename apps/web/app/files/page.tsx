import type { Metadata } from 'next';

import { FilesShell } from '@/components/files/files-shell';

export const metadata: Metadata = {
  title: 'Files',
};

export default function FilesPage() {
  return <FilesShell />;
}
