import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/** Merge conditional class names, resolving conflicting Tailwind utilities. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const;

/** Format a byte count the way Drive does — 1 decimal place above kilobytes. */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) return '—';
  if (bytes === 0) return '0 B';

  const exponent = Math.min(
    Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)),
    BYTE_UNITS.length - 1,
  );
  const value = bytes / 1024 ** exponent;
  const digits = exponent === 0 ? 0 : 1;

  return `${value.toFixed(digits)} ${BYTE_UNITS[exponent]}`;
}

/**
 * Format a Drive timestamp the way a file browser does — a time for today, a
 * month and day for this year, a full date beyond that.
 */
export function formatFileDate(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime()) || date.getTime() === 0) return '—';

  const now = new Date();
  const sameDay =
    date.getDate() === now.getDate() &&
    date.getMonth() === now.getMonth() &&
    date.getFullYear() === now.getFullYear();

  if (sameDay) {
    return date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  }
  if (date.getFullYear() === now.getFullYear()) {
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

/** Percentage of a quota consumed, clamped to 0–100. Null quota means unlimited. */
export function usagePercent(usage: number, limit: number | null): number | null {
  if (limit === null || limit <= 0) return null;
  return Math.min(100, Math.max(0, (usage / limit) * 100));
}
