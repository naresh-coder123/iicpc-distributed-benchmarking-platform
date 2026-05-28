/** @type {import('next').NextConfig} */
const nextConfig = {
  // Produce a standalone build for the Docker image (smaller, self-contained).
  output: 'standalone',

  // Expose API base URLs to the browser bundle.
  // Override at build time via environment variables.
  env: {
    NEXT_PUBLIC_API_BASE:   process.env.NEXT_PUBLIC_API_BASE   || '',
    NEXT_PUBLIC_JUDGE_BASE: process.env.NEXT_PUBLIC_JUDGE_BASE || '',
  },
};

module.exports = nextConfig;
