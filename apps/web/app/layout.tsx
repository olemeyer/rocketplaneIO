import type { Metadata, Viewport } from 'next';
import type { ReactNode } from 'react';
import './globals.css';
import { ThemeProvider, THEME_INIT_SCRIPT } from '@/lib/theme/theme-provider';

export const metadata: Metadata = {
  title: {
    default: 'rocketplane',
    template: '%s · rocketplane',
  },
  description: 'Investigation-first Kubernetes Observability. Connect a cluster in one command.',
  icons: { icon: '/favicon.ico' },
};

export const viewport: Viewport = {
  themeColor: [
    { media: '(prefers-color-scheme: dark)', color: '#0b0b0d' },
    { media: '(prefers-color-scheme: light)', color: '#fbfbfa' },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" data-theme="dark" suppressHydrationWarning>
      <head>
        {/* Setzt data-theme VOR dem ersten Paint → kein FOUC. */}
        <script dangerouslySetInnerHTML={{ __html: THEME_INIT_SCRIPT }} />
      </head>
      <body className="min-h-screen bg-base text-strong antialiased">
        <ThemeProvider>{children}</ThemeProvider>
      </body>
    </html>
  );
}
