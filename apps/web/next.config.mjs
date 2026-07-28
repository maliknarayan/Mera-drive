/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,

  // standalone output keeps the production image small
  output: 'standalone',

  // the shared package ships TypeScript sources during development
  transpilePackages: ['@sangamdrive/shared'],

  eslint: {
    dirs: ['app', 'components', 'lib'],
  },

  images: {
    // Drive thumbnails and Google avatars are the only remote images we render
    remotePatterns: [
      { protocol: 'https', hostname: 'lh3.googleusercontent.com' },
      { protocol: 'https', hostname: 'drive.google.com' },
      { protocol: 'https', hostname: '*.googleusercontent.com' },
    ],
  },

  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          { key: 'X-Frame-Options', value: 'DENY' },
        ],
      },
    ];
  },
};

export default nextConfig;
