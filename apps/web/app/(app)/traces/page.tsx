import type { Metadata } from 'next';
import { TraceList } from '@/components/explore/trace-list';

export const metadata: Metadata = {
  title: 'Traces — rocketplaneIO',
  description: 'Search and filter distributed traces.',
};

export default async function TracesPage({
  searchParams,
}: {
  searchParams: Promise<{ service?: string }>;
}) {
  const { service } = await searchParams;

  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Traces</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Search, filter and drill into distributed traces across your fleet.
        </p>
      </div>
      <TraceList initialService={service ?? ''} />
    </div>
  );
}
