import { defineConfig } from "vitest/config"

export default defineConfig({
  esbuild: { jsx: "automatic" },
  test: { environment: "jsdom", include: ["src/apps/genui/tests/**/*.test.tsx"] },
})
