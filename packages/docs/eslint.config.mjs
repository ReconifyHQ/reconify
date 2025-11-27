// ESLint flat config for Next.js
// Note: Using minimal config to avoid circular reference issues with FlatCompat
export default [
  {
    ignores: [
      'node_modules/**',
      '.next/**',
      'out/**',
      'build/**',
      '.source/**',
      'next-env.d.ts',
      'dist/**',
    ],
  },
];