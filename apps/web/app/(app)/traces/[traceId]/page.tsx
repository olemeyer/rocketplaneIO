import type { Metadata } from 'next';
import { TraceDetail } from '@/components/explore/trace-detail';

export const metadata: Metadata = {
  title: 'Trace — rocketplaneIO',
};

export default async function TracePage({ params }: { params: Promise<{ traceId: string }> }) {
  const { traceId } = await params;
  return <TraceDetail traceId={traceId} />;
}
