# rocketplane web — build from the repo root:
#   docker build -t rocketplane/web:dev -f apps/web/Dockerfile .
#
# NOTE: Next.js freezes rewrites() at build time, so the control-plane origin
# the UI proxies to is baked in via the RP_CONTROLPLANE_URL build arg. The
# default matches the compose service name (http://controlplane:8090).
FROM node:22-alpine AS build
ARG RP_CONTROLPLANE_URL=http://controlplane:8090
ENV RP_CONTROLPLANE_URL=$RP_CONTROLPLANE_URL
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json apps/web/package.json
COPY packages/ui/package.json packages/ui/package.json
COPY packages/tsconfig/package.json packages/tsconfig/package.json
RUN pnpm install --frozen-lockfile --filter @rocketplane/web...
COPY tsconfig.base.json ./
COPY packages/ packages/
COPY apps/web/ apps/web/
RUN pnpm --filter @rocketplane/web build

FROM node:22-alpine
ENV NODE_ENV=production PORT=4173 HOSTNAME=0.0.0.0
WORKDIR /app
# standalone output carries the monorepo shape: apps/web/server.js + pruned node_modules
COPY --from=build /src/apps/web/.next/standalone ./
COPY --from=build /src/apps/web/.next/static ./apps/web/.next/static
USER node
EXPOSE 4173
CMD ["node", "apps/web/server.js"]
