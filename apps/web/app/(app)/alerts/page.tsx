import type { Metadata } from 'next';
import { AlertList } from '@/components/explore/alert-list';

export const metadata: Metadata = {
  title: 'Alerts — rocketplaneIO',
  description: 'Threshold alert rules evaluated live against service health.',
};

export default function AlertsPage() {
  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">Alerts</h1>
        <p className="mt-0.5 text-[13px] text-muted">
          Threshold rules evaluated live against RED service health.
        </p>
      </div>
      <AlertList />
    </div>
  );
}
