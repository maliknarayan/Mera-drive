'use client';

import { TriangleAlert } from 'lucide-react';

import { SessionCard } from '@/components/auth/session-card';
import { SignInCard } from '@/components/auth/sign-in-card';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useSession } from '@/lib/auth';

/** Shows either the sign-in card or the signed-in card. */
export function AuthPanel() {
  const { user, isPending, error } = useSession();

  if (isPending) {
    return (
      <div className="w-full max-w-md space-y-3 rounded-xl border p-6">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <Alert variant="destructive" className="w-full max-w-md">
        <TriangleAlert />
        <div className="min-w-0">
          <AlertTitle>Could not reach the SangamDrive API</AlertTitle>
          <AlertDescription>{error.message}</AlertDescription>
        </div>
      </Alert>
    );
  }

  return user ? <SessionCard user={user} /> : <SignInCard />;
}
