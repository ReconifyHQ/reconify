FROM node:20-alpine AS builder

WORKDIR /app

# Copy workspace files
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/api/package.json ./packages/api/

# Install pnpm
RUN corepack enable && corepack prepare pnpm@latest --activate

# Install dependencies
RUN pnpm install --frozen-lockfile

# Copy source code
COPY packages/api ./packages/api

# Build (when implemented)
# RUN pnpm --filter api build

FROM node:20-alpine

WORKDIR /app

# Copy workspace files
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/api/package.json ./packages/api/

# Install pnpm
RUN corepack enable && corepack prepare pnpm@latest --activate

# Install production dependencies only
RUN pnpm install --frozen-lockfile --prod

# Copy built files and source
COPY packages/api ./packages/api

# Copy CLI binary (must be built first)
# The CLI binary should be built before building this image
# Run: make -C cli build
COPY cli/reconify /usr/local/bin/reconify
RUN chmod +x /usr/local/bin/reconify

EXPOSE 3000

CMD ["pnpm", "--filter", "api", "start"]
