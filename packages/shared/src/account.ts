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
