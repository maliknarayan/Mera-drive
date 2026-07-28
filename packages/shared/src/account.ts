/** Connected Google account types. Mirrors apps/api/internal/store. */

export type DriveScope = 'drive.file' | 'drive';

export type AccountStatus = 'connected' | 'reauth_required' | 'disconnected';

/** Storage numbers come live from the Drive `about` resource. */
export interface StorageQuota {
  /** Total bytes, or null for accounts with unlimited storage. */
  limit: number | null;
  usage: number;
  usage_in_drive: number;
  usage_in_trash: number;
}

export interface ConnectedAccount {
  id: string;
  email: string;
  name: string;
  avatar_url: string;
  scope: DriveScope;
  status: AccountStatus;
  connected_at: string;
  last_used_at: string | null;
  /** Absent when the account is disconnected or the quota call failed. */
  quota?: StorageQuota;
  /** Why the account is unusable, when status is not `connected`. */
  status_reason?: string;
}

/** Aggregate across every connected account, for the dashboard cards. */
export interface StorageSummary {
  total_limit: number | null;
  total_usage: number;
  total_free: number | null;
  account_count: number;
  connected_count: number;
  /** Accounts reporting unlimited storage, excluded from total_limit. */
  unlimited_count: number;
}

export interface CurrentUser {
  id: string;
  email: string;
  name: string;
  avatar_url: string;
}

/**
 * Aggregate the accounts payload into the dashboard totals.
 *
 * The web app derives the summary locally rather than calling `GET /storage`,
 * which would repeat the same fan-out and double the Google API calls needed to
 * render one screen. Mirrors `accounts.Summarise` in the Go API.
 */
export function summariseAccounts(accounts: readonly ConnectedAccount[]): StorageSummary {
  let totalUsage = 0;
  let limited = 0;
  let anyLimited = false;
  let connected = 0;
  let unlimited = 0;

  for (const account of accounts) {
    if (account.status === 'connected') connected += 1;
    if (!account.quota) continue;

    totalUsage += account.quota.usage;

    if (account.quota.limit === null) {
      unlimited += 1;
      continue;
    }
    limited += account.quota.limit;
    anyLimited = true;
  }

  return {
    total_limit: anyLimited ? limited : null,
    total_usage: totalUsage,
    total_free: anyLimited ? Math.max(0, limited - totalUsage) : null,
    account_count: accounts.length,
    connected_count: connected,
    unlimited_count: unlimited,
  };
}

/** Accent colour slot for an account, matching the --drive-N CSS variables. */
export function driveAccent(index: number): string {
  return `var(--drive-${(index % 5) + 1})`;
}

export const ACCOUNT_STATUS_LABELS: Record<AccountStatus, string> = {
  connected: 'Connected',
  reauth_required: 'Reconnect required',
  disconnected: 'Disconnected',
};
