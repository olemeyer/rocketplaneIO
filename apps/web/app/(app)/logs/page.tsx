import type { Metadata } from 'next';
import { LogList } from '@/components/explore/log-list';

export const metadata: Metadata = {
  title: 'Logs — rocketplaneIO',
  description: 'Live, searchable logs correlated with traces.',
};

export default async function LogsPage({
  searchParams,
}: {
  searchParams: Promise<{ service?: string }>;
}) {
  const { service } = await searchParams;

  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Logs</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Live, full-text searchable logs — every line links back to its trace.
        </p>
      </div>
      <LogList initialService={service ?? ''} />
    </div>
  );
}
