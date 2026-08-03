'use client';

import { TriangleAlert } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { type ReactNode, useEffect } from 'react';

import { AppHeader } from '@/components/app-header';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { useSession } from '@/lib/auth';

interface AppShellProps {
  children: ReactNode;
  /** Shown while the session resolves. Page-shaped, so loading never shifts content. */
  fallback: ReactNode;
}

/**
 * Chrome and session gate for every signed-in page.
 *
 * Gating happens in the browser because the session cookie is scoped to the API
 * origin — a Next.js server component cannot see it, and proxying the check
 * through the Next server would put user data somewhere this app keeps clean.
 */
export function AppShell({ children, fallback }: AppShellProps) {
  const { user, isPending, error } = useSession();
  const router = useRouter();

  const signedOut = !isPending && !error && user === null;

  useEffect(() => {
    if (signedOut) router.replace('/');
  }, [signedOut, router]);

  if (error) {
    return (
      <div className="mx-auto max-w-2xl px-4 py-16 sm:px-6">
        <Alert variant="destructive">
          <TriangleAlert />
          <div className="min-w-0">
            <AlertTitle>Could not reach the SangamDrive API</AlertTitle>
            <AlertDescription>{error.message}</AlertDescription>
          </div>
        </Alert>
      </div>
    );
  }

  if (isPending || signedOut || !user) {
    return <div className="mx-auto max-w-7xl px-4 py-10 sm:px-6">{fallback}</div>;
  }

  return (
    <div className="min-h-dvh">
      <AppHeader user={user} />
      <main className="mx-auto max-w-7xl space-y-6 px-4 py-8 sm:px-6">{children}</main>
    </div>
  );
}
