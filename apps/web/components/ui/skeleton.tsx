import { cn } from '@/lib/utils';

/** Placeholder block. Reserves layout space so loading never shifts content. */
export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      aria-hidden
      className={cn(
        'relative overflow-hidden rounded-md bg-muted animate-shimmer',
        className,
      )}
      {...props}
    />
  );
}
