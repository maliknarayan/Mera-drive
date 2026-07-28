import { HardDrive } from 'lucide-react';
import { Suspense } from 'react';

import { ApiStatus } from '@/components/api-status';
import { AuthCallbackBanner } from '@/components/auth/auth-callback-banner';
import { AuthPanel } from '@/components/auth/auth-panel';
import { ThemeToggle } from '@/components/theme-toggle';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';

/** Roadmap shown until the real dashboard lands in phase 3. */
const PHASES = [
  { n: 1, title: 'Project scaffold', done: true },
  { n: 2, title: 'Authentication', done: true },
  { n: 3, title: 'Account management', done: true },
  { n: 4, title: 'Unified browser', done: false },
  { n: 5, title: 'Uploads', done: false },
  { n: 6, title: 'Downloads', done: false },
  { n: 7, title: 'Search', done: false },
  { n: 8, title: 'Previews', done: false },
  { n: 9, title: 'Sharing', done: false },
  { n: 10, title: 'Docker & documentation', done: false },
];

export default function HomePage() {
  return (
    <div className="min-h-dvh">
      <header className="border-b">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-4 sm:px-6">
          <div className="flex items-center gap-2.5">
            <span className="grid size-8 place-items-center rounded-lg bg-primary text-primary-foreground">
              <HardDrive className="size-4" />
            </span>
            <span className="text-base font-semibold tracking-tight">SangamDrive</span>
          </div>
          <ThemeToggle />
        </div>
      </header>

      <main className="mx-auto max-w-5xl px-4 py-10 sm:px-6 sm:py-16">
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
          One dashboard. Unlimited Google Drives.
        </h1>
        <p className="mt-3 max-w-2xl text-balance text-muted-foreground">
          Browse, search and upload across every connected Google account from a single
          interface. Files never touch this server — everything streams straight between your
          browser and Google Drive.
        </p>

        <div className="mt-6">
          <ApiStatus />
        </div>

        {/* useSearchParams needs a boundary so the page can stay statically rendered */}
        <div className="mt-8 max-w-md">
          <Suspense fallback={null}>
            <AuthCallbackBanner />
          </Suspense>
        </div>

        <div className="mt-6 grid items-start gap-6 lg:grid-cols-2">
          <AuthPanel />

          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-1">
            <Card>
              <CardHeader>
                <CardTitle>Build progress</CardTitle>
                <CardDescription>
                  SangamDrive is being built one phase at a time.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <ol className="space-y-1.5 text-sm">
                  {PHASES.map((phase) => (
                    <li key={phase.n} className="flex items-center gap-3">
                      <span
                        className={
                          phase.done
                            ? 'size-1.5 rounded-full bg-[var(--success)]'
                            : 'size-1.5 rounded-full bg-muted-foreground/40'
                        }
                        aria-hidden
                      />
                      <span className={phase.done ? '' : 'text-muted-foreground'}>
                        Phase {phase.n} — {phase.title}
                      </span>
                      {phase.done && <span className="sr-only">complete</span>}
                    </li>
                  ))}
                </ol>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>What gets stored</CardTitle>
                <CardDescription>
                  SangamDrive is a frontend over the Drive API, not storage.
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4 text-sm">
                <div>
                  <p className="font-medium">Stored on this server</p>
                  <ul className="mt-1 list-inside list-disc text-muted-foreground">
                    <li>Your user record</li>
                    <li>Encrypted Google refresh tokens</li>
                    <li>UI preferences and sessions</li>
                  </ul>
                </div>
                <div>
                  <p className="font-medium">Never stored</p>
                  <ul className="mt-1 list-inside list-disc text-muted-foreground">
                    <li>File contents</li>
                    <li>File metadata or thumbnails</li>
                    <li>Search indexes</li>
                  </ul>
                </div>
              </CardContent>
            </Card>
          </div>
        </div>
      </main>
    </div>
  );
}
