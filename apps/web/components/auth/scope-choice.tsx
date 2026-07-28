'use client';

import { type DriveScope, SCOPE_DESCRIPTIONS } from '@sangamdrive/shared';

import { cn } from '@/lib/utils';

const SCOPES: DriveScope[] = ['drive.file', 'drive'];

interface ScopeChoiceProps {
  value: DriveScope;
  onChange: (scope: DriveScope) => void;
  name: string;
}

/** Radio group for choosing how much Drive access to grant an account. */
export function ScopeChoice({ value, onChange, name }: ScopeChoiceProps) {
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Permissions for this account</legend>

      {SCOPES.map((scope) => {
        const { label, detail } = SCOPE_DESCRIPTIONS[scope];
        const selected = value === scope;

        return (
          <label
            key={scope}
            className={cn(
              'flex cursor-pointer gap-3 rounded-lg border p-3 transition-colors',
              selected ? 'border-primary bg-accent' : 'hover:bg-accent/50',
            )}
          >
            <input
              type="radio"
              name={name}
              value={scope}
              checked={selected}
              onChange={() => onChange(scope)}
              className="mt-1 size-4 shrink-0 accent-[var(--primary)]"
            />
            <span className="min-w-0">
              <span className="block text-sm font-medium">
                {label}
                {scope === 'drive.file' && (
                  <span className="ml-2 rounded bg-secondary px-1.5 py-0.5 text-xs font-normal text-secondary-foreground">
                    recommended
                  </span>
                )}
              </span>
              <span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span>
            </span>
          </label>
        );
      })}

      <p className="pt-1 text-xs text-muted-foreground">
        You can upgrade an account to full access later without disconnecting it.
      </p>
    </fieldset>
  );
}
