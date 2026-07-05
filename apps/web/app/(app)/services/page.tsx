import type { Metadata } from 'next';
import { ServiceCatalog } from '@/components/explore/service-catalog';
import { ServiceMap } from '@/components/explore/service-map';

export const metadata: Metadata = {
  title: 'Services — rocketplaneIO',
  description: 'Service catalog and dependency map with RED health.',
};

export default function ServicesPage() {
  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Services</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Live dependency map and RED health for every service — click any node for detail.
        </p>
      </div>

      <section>
        <h2 className="mb-2 font-mono text-[11px] uppercase tracking-wider text-faint">Service map</h2>
        <ServiceMap />
      </section>

      <section>
        <h2 className="mb-2 font-mono text-[11px] uppercase tracking-wider text-faint">Catalog</h2>
        <ServiceCatalog />
      </section>
    </div>
  );
}
