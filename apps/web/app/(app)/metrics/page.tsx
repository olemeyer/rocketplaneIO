import type { Metadata } from 'next';
import { MetricExplorer } from '@/components/explore/metric-explorer';

export const metadata: Metadata = {
  title: 'Metrics — rocketplaneIO',
  description: 'Explore OpenTelemetry metrics.',
};

export default async function MetricsPage({
  searchParams,
}: {
  searchParams: Promise<{ metric?: string }>;
}) {
  const { metric } = await searchParams;

  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Metrics</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Explore OpenTelemetry gauge and sum metrics, broken down per service.
        </p>
      </div>
      <MetricExplorer initial={metric ?? ''} />
    </div>
  );
}
