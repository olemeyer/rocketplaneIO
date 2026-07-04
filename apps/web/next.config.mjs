/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@rocketplane/ui'], // Workspace-Paket wird als TS-Source konsumiert
  eslint: { ignoreDuringBuilds: true }, // Lint läuft separat via tsc/CI

  // Self-Hosting: Für den Docker-Build in M0 hier `output: 'standalone'` aktivieren
  // (erzeugt einen minimalen Node-Server-Bundle). Lokal wird `next start` genutzt,
  // das nicht mit 'standalone' kombiniert werden sollte.
};

export default nextConfig;
