import { cn } from '@/lib/utils';

interface ProgressProps {
  /** 0–100, or null for an indeterminate/unlimited quota. */
  value: number | null;
  label: string;
  /** CSS colour for the filled portion. */
  accent?: string;
  className?: string;
}

/** Thin usage meter. Turns amber past 80% and red past 95%. */
export function Progress({ value, label, accent, className }: ProgressProps) {
  if (value === null) {
    return (
      <div
        className={cn('h-1.5 w-full overflow-hidden rounded-full bg-muted', className)}
        role="img"
        aria-label={`${label}: unlimited`}
      />
    );
  }

  const clamped = Math.min(100, Math.max(0, value));
  const colour = accent ?? severityColour(clamped);

  return (
    <div
      className={cn('h-1.5 w-full overflow-hidden rounded-full bg-muted', className)}
      role="progressbar"
      aria-valuenow={Math.round(clamped)}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={label}
    >
      <div
        className="h-full rounded-full transition-[width] duration-500"
        style={{ width: `${clamped}%`, backgroundColor: colour }}
      />
    </div>
  );
}

function severityColour(percent: number): string {
  if (percent >= 95) return 'var(--destructive)';
  if (percent >= 80) return 'var(--warning)';
  return 'var(--primary)';
}
