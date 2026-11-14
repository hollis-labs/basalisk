import { designConfig } from "@hollis-labs/eslint-config-design"
import tseslint from "typescript-eslint"

// Biome owns general lint/formatting; this lane enforces design-kit's token contract.
export default [
  { files: ["src/**/*.{ts,tsx}"], languageOptions: { parser: tseslint.parser } },
  ...(await designConfig({
    files: ["src/**/*.{ts,tsx}"],
    // The Diagram's looks are written into the drawing so an export matches the screen.
    themeFiles: ["src/apps/diagram/theme.ts"],
  })),
]
