FROM node:20-alpine AS builder

WORKDIR /app

# Copy workspace files
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/docs/package.json ./packages/docs/

# Install pnpm
RUN corepack enable && corepack prepare pnpm@latest --activate

# Install dependencies
RUN pnpm install --frozen-lockfile

# Copy source code
COPY packages/docs ./packages/docs

# Build
RUN pnpm --filter docs build

FROM node:20-alpine

WORKDIR /app

# Copy built files
COPY --from=builder /app/packages/docs/.next/standalone ./
COPY --from=builder /app/packages/docs/.next/static ./packages/docs/.next/static
COPY --from=builder /app/packages/docs/public ./packages/docs/public

EXPOSE 3000

ENV NODE_ENV=production
ENV PORT=3000

CMD ["node", "packages/docs/server.js"]

