import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  async redirects() {
    return [
      { source: '/docs', destination: '/docs/cli', permanent: false },
      { source: '/docs/getting-started', destination: '/docs/cli/quickstart', permanent: true },
      { source: '/docs/configuration', destination: '/docs/cli/reference/config', permanent: true },
      { source: '/docs/engine', destination: '/docs/cli/engine', permanent: true },
      { source: '/docs/engine/:path*', destination: '/docs/cli/engine/:path*', permanent: true },
      { source: '/docs/performance', destination: '/docs/cli/performance', permanent: true },
      { source: '/docs/performance/:path*', destination: '/docs/cli/performance/:path*', permanent: true },
    ];
  },
};

export default withMDX(config);
