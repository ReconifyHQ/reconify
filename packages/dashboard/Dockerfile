FROM node:20-alpine AS builder

WORKDIR /app

# Copy workspace files
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/dashboard/package.json ./packages/dashboard/

# Install pnpm
RUN corepack enable && corepack prepare pnpm@latest --activate

# Install dependencies
RUN pnpm install --frozen-lockfile

# Copy source code
COPY packages/dashboard ./packages/dashboard

# Build (when implemented)
# RUN pnpm --filter dashboard build

FROM nginx:alpine

# Copy built files (when implemented)
# COPY --from=builder /app/packages/dashboard/dist /usr/share/nginx/html

# For now, create a simple placeholder
RUN echo '<!DOCTYPE html><html><head><title>Reconify Dashboard</title></head><body><h1>Reconify Dashboard</h1><p>Coming soon...</p></body></html>' > /usr/share/nginx/html/index.html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
