'use client';

import type { DriveScope } from '@sangamdrive/shared';
import { useState } from 'react';

import { ScopeChoice } from '@/components/auth/scope-choice';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { googleAuthUrl } from '@/lib/auth';
import { cn } from '@/lib/utils';
import { buttonVariants } from '@/components/ui/button';

/** Entry point for a first sign-in. */
export function SignInCard() {
  const [scope, setScope] = useState<DriveScope>('drive.file');

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle className="text-lg">Sign in with Google</CardTitle>
        <CardDescription>
          The account you sign in with becomes your SangamDrive identity. You can connect more
          Google accounts afterwards.
        </CardDescription>
      </CardHeader>

      <CardContent className="space-y-5">
        <ScopeChoice name="signin-scope" value={scope} onChange={setScope} />

        {/* a real navigation, not a fetch — the server must set an HttpOnly cookie */}
        <a
          href={googleAuthUrl({ intent: 'login', scope })}
          className={cn(buttonVariants({ size: 'lg' }), 'w-full')}
        >
          <GoogleMark />
          Continue with Google
        </a>

        <p className="text-center text-xs text-muted-foreground">
          SangamDrive never stores your files or their metadata.
        </p>
      </CardContent>
    </Card>
  );
}

function GoogleMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden className="size-4">
      <path
        fill="currentColor"
        d="M12 11v2.4h5.6c-.24 1.5-1.76 4.4-5.6 4.4A6.1 6.1 0 0 1 12 5.8c1.86 0 3.1.79 3.82 1.47l1.9-1.83A8.6 8.6 0 0 0 12 3a9 9 0 0 0 0 18c5.2 0 8.62-3.65 8.62-8.79 0-.6-.06-1.06-.15-1.51H12Z"
      />
    </svg>
  );
}
