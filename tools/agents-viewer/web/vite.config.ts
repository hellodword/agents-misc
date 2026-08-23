import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            {
              name: "react-core",
              test: /node_modules[\\/](?:react|react-dom|scheduler)[\\/]/,
              priority: 40,
            },
            {
              name: "router",
              test: /node_modules[\\/](?:react-router|react-router-dom)[\\/]/,
              priority: 30,
            },
            {
              name: "markdown",
              test: /node_modules[\\/](?:react-markdown|rehype-highlight|rehype-sanitize|remark-gfm)[\\/]/,
              priority: 20,
            },
            {
              name: "ui",
              test: /node_modules[\\/](?:@radix-ui[\\/]|@tanstack[\\/]react-virtual[\\/]|cmdk[\\/]|lucide-react[\\/]|radix-ui[\\/]|react-resizable-panels[\\/])/,
              priority: 10,
            },
            {
              name: "vendor",
              test: /node_modules[\\/]/,
            },
          ],
        },
      },
    },
  },
  server: { proxy: { "/api": "http://127.0.0.1:4747" } },
  test: {
    include: ["src/**/*.test.{ts,tsx}", "e2e/browser.test.ts"],
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
});
