import type { Metadata } from 'next';
import { ServiceDetailView } from '@/components/explore/service-detail-view';

export const metadata: Metadata = {
  title: 'Service — rocketplaneIO',
};

export default async function ServicePage({ params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  return <ServiceDetailView name={decodeURIComponent(name)} />;
}
