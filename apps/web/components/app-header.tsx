'use client';

import type { SessionResponse } from '@sangamdrive/shared';
import { FolderOpen, HardDrive, LogOut, Plus } from 'lucide-react';
import Image from 'next/image';
import Link from 'next/link';
import { usePathname } from 'next/navigation';

import { ThemeToggle } from '@/components/theme-toggle';
import { Button, buttonVariants } from '@/components/ui/button';
import { googleAuthUrl, useLogout } from '@/lib/auth';
import { cn } from '@/lib/utils';

interface AppHeaderProps {
  user: SessionResponse['user'];
}

const NAV = [
  { href: '/dashboard', label: 'Storage', icon: HardDrive },
  { href: '/files', label: 'Files', icon: FolderOpen },
];

export function AppHeader({ user }: AppHeaderProps) {
  const logout = useLogout();
  const pathname = usePathname();

  return (
    <header className="sticky top-0 z-10 border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <div className="mx-auto flex max-w-7xl items-center gap-3 px-4 py-3 sm:px-6">
        <Link href="/dashboard" className="flex items-center gap-2.5">
          <span className="grid size-8 place-items-center rounded-lg bg-primary text-primary-foreground">
            <HardDrive className="size-4" />
          </span>
          <span className="hidden text-base font-semibold tracking-tight sm:inline">
            SangamDrive
          </span>
        </Link>

        <nav aria-label="Sections" className="flex items-center gap-0.5 sm:ml-4">
          {NAV.map(({ href, label, icon: Icon }) => {
            const current = pathname === href;
            return (
              <Link
                key={href}
                href={href}
                aria-current={current ? 'page' : undefined}
                className={cn(
                  'flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors',
                  current
                    ? 'bg-secondary text-secondary-foreground'
                    : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                )}
              >
                <Icon className="size-4" />
                {label}
              </Link>
            );
          })}
        </nav>

        <div className="ml-auto flex items-center gap-2">
          <a
            href={googleAuthUrl({ intent: 'link', scope: 'drive.file', next: '/dashboard' })}
            className={cn(buttonVariants({ size: 'sm' }))}
          >
            <Plus />
            <span className="hidden sm:inline">Connect account</span>
          </a>

          <ThemeToggle />

          <span className="hidden items-center gap-2 border-l pl-2 sm:flex">
            {user.avatar_url ? (
              <Image
                src={user.avatar_url}
                alt=""
                width={28}
                height={28}
                className="size-7 rounded-full"
              />
            ) : (
              <span className="grid size-7 place-items-center rounded-full bg-secondary text-xs font-medium">
                {user.email.slice(0, 1).toUpperCase()}
              </span>
            )}
            <span className="max-w-[12rem] truncate text-sm text-muted-foreground">
              {user.email}
            </span>
          </span>

          <Button
            variant="ghost"
            size="icon"
            aria-label="Sign out"
            title="Sign out"
            disabled={logout.isPending}
            onClick={() => logout.mutate('this')}
          >
            <LogOut />
          </Button>
        </div>
      </div>
    </header>
  );
}
