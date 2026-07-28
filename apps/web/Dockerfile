# syntax=docker/dockerfile:1

# Built from the repository root so the shared workspace package is available:
#   docker build -f apps/web/Dockerfile .

FROM node:22-alpine AS deps
WORKDIR /repo

COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/
COPY packages/shared/package.json packages/shared/
RUN npm ci

FROM node:22-alpine AS build
WORKDIR /repo

# baked into the client bundle, so it must be known at build time
ARG NEXT_PUBLIC_API_BASE_URL=http://localhost:8080
ENV NEXT_PUBLIC_API_BASE_URL=${NEXT_PUBLIC_API_BASE_URL}
ENV NEXT_TELEMETRY_DISABLED=1

COPY --from=deps /repo/node_modules ./node_modules
COPY . .

RUN npm run build --workspace packages/shared \
    && npm run build --workspace apps/web

FROM node:22-alpine AS runtime
WORKDIR /app

ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

RUN addgroup -g 10001 -S sangam && adduser -u 10001 -S sangam -G sangam

# next's standalone output already contains a pruned node_modules
COPY --from=build --chown=sangam:sangam /repo/apps/web/.next/standalone ./
COPY --from=build --chown=sangam:sangam /repo/apps/web/.next/static ./apps/web/.next/static
COPY --from=build --chown=sangam:sangam /repo/apps/web/public ./apps/web/public

USER sangam
EXPOSE 3000

CMD ["node", "apps/web/server.js"]
