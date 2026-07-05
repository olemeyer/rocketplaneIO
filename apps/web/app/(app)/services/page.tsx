import type { Metadata } from 'next';
import { ServiceCatalog } from '@/components/explore/service-catalog';

export const metadata: Metadata = {
  title: 'Services — rocketplaneIO',
  description: 'Service catalog with RED health.',
};

export default function ServicesPage() {
  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Services</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Every service in your fleet with live RED health — click through for detail.
        </p>
      </div>
      <ServiceCatalog />
    </div>
  );
}
