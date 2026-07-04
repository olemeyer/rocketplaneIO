/** @type {import('next').NextConfig} */

// Backend-Origin des query-Service (Compose: http://query:7080). Der Browser
// fetcht ausschliesslich same-origin /api/rp/* — der Next-Server proxied weiter,
// daher KEIN CORS im Browser nötig.
const QUERY_ORIGIN = process.env.RP_QUERY_ORIGIN ?? 'http://localhost:7080';

const nextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@rocketplane/ui'], // Workspace-Paket wird als TS-Source konsumiert
  eslint: { ignoreDuringBuilds: true }, // Lint läuft separat via tsc/CI

  async rewrites() {
    return [
      // /healthz liegt am query-Service auf ROOT (nicht unter /api) -> ZUERST.
      { source: '/api/rp/healthz', destination: `${QUERY_ORIGIN}/healthz` },
      // Explore-/Prometheus-API: /api/rp/v1/* -> :7080/api/v1/*
      { source: '/api/rp/:path*', destination: `${QUERY_ORIGIN}/api/:path*` },
    ];
  },

  // Self-Hosting: Für den Docker-Build in M0 hier `output: 'standalone'` aktivieren.
};

export default nextConfig;
