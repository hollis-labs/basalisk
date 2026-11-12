import { writeFileSync } from "node:fs"
import { resolve } from "node:path"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

const outDir = resolve(import.meta.dirname, "../internal/webui/dist")

export default defineConfig({
  base: "/",
  plugins: [
    tailwindcss(),
    {
      // emptyOutDir removes dist/.gitkeep, which the Go embed needs to compile when unbuilt.
      name: "keep-gitkeep",
      closeBundle() {
        writeFileSync(resolve(outDir, ".gitkeep"), "")
      },
    },
  ],
  esbuild: { jsx: "automatic" },
  build: { outDir, emptyOutDir: true },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8085",
      "/auth": "http://127.0.0.1:8085",
      "/companion": "http://127.0.0.1:8085",
    },
  },
})
