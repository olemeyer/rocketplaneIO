import type { Metadata } from 'next';
import { LoginForm } from '@/components/auth/login-form';
import { Wordmark } from '@/components/brand/wordmark';

export const metadata: Metadata = {
  title: 'Sign in — rocketplaneIO',
};

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ next?: string }>;
}) {
  const { next } = await searchParams;
  const target = next && next.startsWith('/') ? next : '/explore';

  return (
    <div className="grain relative grid min-h-screen place-items-center px-6">
      <div className="aurora opacity-60" aria-hidden />
      <div className="relative w-full max-w-sm">
        <a href="/" className="mb-8 flex items-center gap-2.5">
          <span className="beacon h-7 w-7 rounded-lg" aria-hidden />
          <Wordmark className="font-display text-lg font-semibold tracking-tight text-strong" />
        </a>
        <LoginForm next={target} />
        <p className="mt-6 text-center text-[12px] text-faint">
          Self-hosted, OpenTelemetry-native observability.
        </p>
      </div>
    </div>
  );
}
