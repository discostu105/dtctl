import { defineConfig } from 'vitest/config'

// Unit tests for the pure libraries (src/**/*.test.ts). The Playwright smoke
// suite lives in e2e/ and runs separately (`npm run e2e`).
export default defineConfig({
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
})
