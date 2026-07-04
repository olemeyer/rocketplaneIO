import type { Metadata, Viewport } from 'next';
import type { CSSProperties, ReactNode } from 'react';
import { cssVars } from '@rocketplane/ui';
import './globals.css';

export const metadata: Metadata = {
  title: 'rocketplaneIO — observability engineers love',
  description:
    'Open-source, OpenTelemetry-native observability with a keyboard-first UI. Traces, metrics, logs, RUM and synthetics on one canvas. Self-hostable, no lock-in.',
  applicationName: 'rocketplaneIO',
};

export const viewport: Viewport = {
  themeColor: '#0a0e14',
  colorScheme: 'dark',
};

export default function RootLayout({ children }: { children: ReactNode }) {
  // Theme-Variablen serverseitig aus den Tokens setzen (Dark als Default).
  const darkVars = cssVars('dark') as CSSProperties;
  return (
    <html lang="en" data-theme="dark" style={darkVars} suppressHydrationWarning>
      <body>{children}</body>
    </html>
  );
}
