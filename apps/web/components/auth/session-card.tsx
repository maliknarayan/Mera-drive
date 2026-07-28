'use client';

import type { DriveScope, SessionResponse } from '@sangamdrive/shared';
import { LogOut, Plus } from 'lucide-react';
import Image from 'next/image';
import { useState } from 'react';

import { ScopeChoice } from '@/components/auth/scope-choice';
import { Button, buttonVariants } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { useLogout } from '@/lib/auth';
import { googleAuthUrl } from '@/lib/auth';
import { cn } from '@/lib/utils';

interface SessionCardProps {
  user: SessionResponse['user'];
}

/** Signed-in state: who you are, plus the account actions phase 2 delivers. */
export function SessionCard({ user }: SessionCardProps) {
  const [scope, setScope] = useState<DriveScope>('drive.file');
  const logout = useLogout();

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <div className="flex items-center gap-3">
          {user.avatar_url ? (
            <Image
              src={user.avatar_url}
              alt=""
              width={40}
              height={40}
              className="size-10 rounded-full"
            />
          ) : (
            <span className="grid size-10 place-items-center rounded-full bg-secondary text-sm font-medium">
              {user.email.slice(0, 1).toUpperCase()}
            </span>
          )}
          <div className="min-w-0">
            <CardTitle className="truncate text-base">{user.name || user.email}</CardTitle>
            <CardDescription className="truncate">{user.email}</CardDescription>
          </div>
        </div>
      </CardHeader>

      <CardContent className="space-y-5">
        <ScopeChoice name="link-scope" value={scope} onChange={setScope} />

        <a
          href={googleAuthUrl({ intent: 'link', scope })}
          className={cn(buttonVariants(), 'w-full')}
        >
          <Plus />
          Connect another Google account
        </a>

        <div className="flex flex-wrap gap-2 border-t pt-4">
          <Button
            variant="outline"
            onClick={() => logout.mutate('this')}
            disabled={logout.isPending}
          >
            <LogOut />
            Sign out
          </Button>
          <Button
            variant="ghost"
            onClick={() => logout.mutate('all')}
            disabled={logout.isPending}
          >
            Sign out everywhere
          </Button>
        </div>

        {logout.error && (
          <p className="text-sm text-destructive">{logout.error.message}</p>
        )}
      </CardContent>
    </Card>
  );
}
