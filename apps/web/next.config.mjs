import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** @type {import('next').NextConfig} */

// Zwei Backend-Origins:
//  - Control-Plane (Go, :8090): Auth + Tenancy + Cluster/Agent-API. Der Browser
//    fetcht ausschliesslich SAME-ORIGIN /api/* und /auth/* — der Next-Server
//    proxied weiter. Same-origin ist zwingend, damit das HttpOnly-Cookie
//    `rp_session` gesetzt/mitgesendet wird (kein CORS, kein Cookie-Verlust).
//  - Query-Service (:7080): Observability-Reads unter /api/rp/* (bleibt getrennt).
//
// ACHTUNG Self-Hosting: rewrites() wird beim `next build` EINGEFROREN — im
// Docker-Image gilt der Wert von RP_CONTROLPLANE_URL zur BUILD-Zeit (Build-Arg,
// Default http://controlplane:8090 = Compose-Servicename), nicht zur Laufzeit.
const CONTROLPLANE_ORIGIN = process.env.RP_CONTROLPLANE_URL ?? 'http://localhost:8090';
const QUERY_ORIGIN = process.env.RP_QUERY_ORIGIN ?? 'http://localhost:7080';

const nextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@rocketplane/ui'], // Workspace-Paket wird als TS-Source konsumiert
  eslint: { ignoreDuringBuilds: true }, // Lint läuft separat via tsc/CI

  // Self-Hosting: minimaler Server-Output fürs Docker-Image; im Monorepo muss
  // das File-Tracing vom Workspace-Root aus laufen.
  output: 'standalone',
  outputFileTracingRoot: path.join(path.dirname(fileURLToPath(import.meta.url)), '../../'),

  async rewrites() {
    return [
      // Query-Service ZUERST (spezifischer als /api/:path*). Erste Übereinstimmung gewinnt.
      { source: '/api/rp/healthz', destination: `${QUERY_ORIGIN}/healthz` },
      { source: '/api/rp/:path*', destination: `${QUERY_ORIGIN}/api/:path*` },
      // Control-Plane: alle übrigen /api/* + /auth/* same-origin proxien.
      { source: '/api/:path*', destination: `${CONTROLPLANE_ORIGIN}/api/:path*` },
      { source: '/auth/:path*', destination: `${CONTROLPLANE_ORIGIN}/auth/:path*` },
    ];
  },
};

export default nextConfig;
