import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

export default defineConfig({
  base: "/",
  plugins: [tailwindcss()],
  esbuild: { jsx: "automatic" },
  build: { outDir: "../internal/webui/dist", emptyOutDir: true },
  server: { proxy: { "/api": "http://127.0.0.1:8080" } },
})
