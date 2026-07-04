import type { Metadata } from 'next';
import { FilterBar, ServiceHealthPanel } from '@/components/explore/service-health';
import { TraceWaterfall } from '@/components/explore/trace-waterfall';

export const metadata: Metadata = {
  title: 'Explore — rocketplane',
  description: 'Live service health and distributed traces.',
};

export default function ExplorePage() {
  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Explore</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Live service health and the latest distributed traces across your fleet.
        </p>
      </div>

      <FilterBar />
      <ServiceHealthPanel />
      <TraceWaterfall />
    </div>
  );
}
