'use client';

import { AUTH_CALLBACK_KEYS, type AuthIntent, type ErrorCode } from '@sangamdrive/shared';
import { CheckCircle2, TriangleAlert } from 'lucide-react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useState } from 'react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';

const SUCCESS_COPY: Record<AuthIntent, string> = {
  login: 'Signed in',
  link: 'Google account connected',
  reconnect: 'Account reconnected',
  upgrade: 'Permissions upgraded',
};

/** Copy for the codes the OAuth callback can realistically return. */
const ERROR_COPY: Partial<Record<ErrorCode, string>> = {
  bad_request: 'Sign-in could not be completed',
  forbidden: 'Access was not granted',
  conflict: 'Wrong Google account',
  insufficient_scope: 'Permission was not granted',
  reauth_required: 'Google rejected the sign-in',
  rate_limited: 'Too many attempts',
  upstream_unavailable: 'Google is unreachable',
  internal_error: 'Something went wrong',
};

/**
 * Renders the outcome of an OAuth round trip, then strips the parameters from
 * the URL so a refresh or a shared link does not replay a stale message.
 */
export function AuthCallbackBanner() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const [outcome, setOutcome] = useState<{
    kind: 'success' | 'error';
    title: string;
    detail: string;
  } | null>(null);

  useEffect(() => {
    const intent = searchParams.get('auth') as AuthIntent | null;
    const errorCode = searchParams.get('auth_error') as ErrorCode | null;

    if (!intent && !errorCode) return;

    if (errorCode) {
      setOutcome({
        kind: 'error',
        title: ERROR_COPY[errorCode] ?? 'Sign-in failed',
        detail: searchParams.get('auth_message') ?? 'Please try again.',
      });
    } else if (intent) {
      const account = searchParams.get('account');
      setOutcome({
        kind: 'success',
        title: SUCCESS_COPY[intent] ?? 'Done',
        detail: account ? `Connected as ${account}.` : '',
      });
    }

    const remaining = new URLSearchParams(searchParams.toString());
    for (const key of AUTH_CALLBACK_KEYS) remaining.delete(key);

    const query = remaining.toString();
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  }, [searchParams, pathname, router]);

  if (!outcome) return null;

  return (
    <Alert variant={outcome.kind === 'success' ? 'success' : 'destructive'}>
      {outcome.kind === 'success' ? <CheckCircle2 /> : <TriangleAlert />}
      <div className="min-w-0">
        <AlertTitle>{outcome.title}</AlertTitle>
        {outcome.detail && <AlertDescription>{outcome.detail}</AlertDescription>}
      </div>
    </Alert>
  );
}
