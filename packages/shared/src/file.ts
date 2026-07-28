/**
 * Drive file types.
 *
 * These are projections of the Google Drive API v3 `files` resource, never
 * persisted anywhere — the API forwards them straight from Google to the
 * browser on each request.
 */

export const FOLDER_MIME_TYPE = 'application/vnd.google-apps.folder';

export const GOOGLE_DOC_MIME_TYPES = {
  document: 'application/vnd.google-apps.document',
  spreadsheet: 'application/vnd.google-apps.spreadsheet',
  presentation: 'application/vnd.google-apps.presentation',
  drawing: 'application/vnd.google-apps.drawing',
  form: 'application/vnd.google-apps.form',
} as const;

export type FileKind =
  | 'folder'
  | 'image'
  | 'video'
  | 'audio'
  | 'pdf'
  | 'text'
  | 'archive'
  | 'gdoc'
  | 'gsheet'
  | 'gslide'
  | 'other';

export interface DriveFileOwner {
  display_name: string;
  email: string;
  photo_url: string;
}

export interface DriveFile {
  id: string;
  name: string;
  mime_type: string;
  kind: FileKind;
  /** Bytes. Absent for Google-native files, which report no size. */
  size: number | null;
  modified_at: string;
  created_at: string;
  starred: boolean;
  trashed: boolean;
  shared: boolean;
  /** IDs of the containing folders, as returned by Drive. */
  parents: string[];
  web_view_link: string;
  icon_link: string;
  thumbnail_link: string | null;
  owner: DriveFileOwner | null;

  /** SangamDrive account this file belongs to — the key to unified browsing. */
  account_id: string;
  account_email: string;
}

export interface Breadcrumb {
  id: string;
  name: string;
}

export type ViewMode = 'grid' | 'list';

export type SortField = 'name' | 'modified_at' | 'size' | 'account_email';

export type SortDirection = 'asc' | 'desc';

export interface SortSpec {
  field: SortField;
  direction: SortDirection;
}

export function isFolder(file: Pick<DriveFile, 'mime_type'>): boolean {
  return file.mime_type === FOLDER_MIME_TYPE;
}

export function isGoogleNative(file: Pick<DriveFile, 'mime_type'>): boolean {
  return file.mime_type.startsWith('application/vnd.google-apps.');
}

/** Map a MIME type to the coarse category the UI uses for icons and previews. */
export function fileKindFromMimeType(mimeType: string): FileKind {
  if (mimeType === FOLDER_MIME_TYPE) return 'folder';
  if (mimeType === GOOGLE_DOC_MIME_TYPES.document) return 'gdoc';
  if (mimeType === GOOGLE_DOC_MIME_TYPES.spreadsheet) return 'gsheet';
  if (mimeType === GOOGLE_DOC_MIME_TYPES.presentation) return 'gslide';
  if (mimeType === 'application/pdf') return 'pdf';
  if (mimeType.startsWith('image/')) return 'image';
  if (mimeType.startsWith('video/')) return 'video';
  if (mimeType.startsWith('audio/')) return 'audio';
  if (mimeType.startsWith('text/')) return 'text';
  if (ARCHIVE_MIME_TYPES.has(mimeType)) return 'archive';
  return 'other';
}

const ARCHIVE_MIME_TYPES = new Set([
  'application/zip',
  'application/x-tar',
  'application/gzip',
  'application/x-7z-compressed',
  'application/vnd.rar',
]);
