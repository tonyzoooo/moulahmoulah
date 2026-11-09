import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "@playwright/test";

const e2eDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(e2eDir, "..");
const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:5173";

// Load KEY=VALUE pairs from the repo-root .env (gitignored) so the backend
// webServer gets its Stripe test key without extra tooling.
function loadDotEnv(file: string): Record<string, string> {
  const out: Record<string, string> = {};
  if (!fs.existsSync(file)) return out;
  for (const line of fs.readFileSync(file, "utf8").split(/\r?\n/)) {
    const m = /^([A-Z0-9_]+)=(.*)$/.exec(line.trim());
    if (m) out[m[1]] = m[2].replace(/^["']|["']$/g, "");
  }
  return out;
}

const dotEnv = loadDotEnv(path.join(repoRoot, ".env"));
const stripeKey = process.env.STRIPE_SECRET_KEY ?? dotEnv.STRIPE_SECRET_KEY ?? "";
if (!stripeKey.startsWith("sk_test_")) {
  throw new Error(
    "A Stripe TEST secret key is required to run the e2e suite. " +
      "Copy .env.example to .env at the repo root and set STRIPE_SECRET_KEY=sk_test_...",
  );
}

// Video recording: every run is recorded by default into
// e2e/test-results/<spec>/<test>/video.webm. Override with E2E_VIDEO=off|retain-on-failure.
const video = (process.env.E2E_VIDEO ?? "on") as "on" | "off" | "retain-on-failure";

export default defineConfig({
  testDir: "./tests",
  outputDir: "./test-results",
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1, // one backend, one Stripe test account: run serially
  retries: process.env.CI ? 2 : 0,
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "./playwright-report" }],
  ],
  use: {
    baseURL,
    video,
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
  webServer: [
    {
      // Go backend on :3000 (serves /api; needs the Stripe test key).
      command: "go run .",
      cwd: path.join(repoRoot, "backend"),
      url: "http://localhost:3000/api/catalog",
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
      stdout: "ignore",
      stderr: "pipe",
      env: { ...process.env, ...dotEnv, BASE_URL: baseURL },
    },
    {
      // Vite dev server on :5173 (serves the app, proxies /api to :3000).
      command: "npm run dev",
      cwd: path.join(repoRoot, "frontend"),
      url: baseURL,
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
      stdout: "ignore",
      stderr: "pipe",
    },
  ],
});
