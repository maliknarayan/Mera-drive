'use client';

import { Monitor, Moon, Sun } from 'lucide-react';
import { useTheme } from 'next-themes';
import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

const THEMES = [
  { value: 'light', label: 'Light', Icon: Sun },
  { value: 'dark', label: 'Dark', Icon: Moon },
  { value: 'system', label: 'System', Icon: Monitor },
] as const;

export function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  // theme is unknown until hydration, so render a stable placeholder first
  useEffect(() => setMounted(true), []);

  if (!mounted) {
    return <div className="h-8 w-[6.75rem] rounded-md border" aria-hidden />;
  }

  return (
    <div role="radiogroup" aria-label="Colour theme" className="flex items-center gap-0.5 rounded-md border p-0.5">
      {THEMES.map(({ value, label, Icon }) => (
        <Button
          key={value}
          role="radio"
          aria-checked={theme === value}
          aria-label={label}
          title={label}
          variant="ghost"
          size="icon"
          className={cn('size-7', theme === value && 'bg-accent text-accent-foreground')}
          onClick={() => setTheme(value)}
        >
          <Icon />
        </Button>
      ))}
    </div>
  );
}
