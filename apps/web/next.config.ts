import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { NextConfig } from "next";

// The root package.json is the single release version for the web app,
// API, and CLI; builds inject the same value into the Go binaries. Next may
// run from the repository root or from apps/web, so search upward.
function releaseVersion() {
  let directory = process.cwd();
  for (let depth = 0; depth < 4; depth += 1) {
    try {
      const manifest = JSON.parse(
        readFileSync(join(directory, "package.json"), "utf8"),
      ) as { name?: string; version?: string };
      if (manifest.name === "gitown" && manifest.version)
        return manifest.version;
    } catch {
      // Keep looking in the parent directory.
    }
    directory = dirname(directory);
  }
  return "dev";
}

const config: NextConfig = {
  env: {
    NEXT_PUBLIC_GITOWN_VERSION: releaseVersion(),
    NEXT_PUBLIC_GITOWN_CHANNEL:
      process.env.NEXT_PUBLIC_GITOWN_CHANNEL || "alpha",
  },
  agentRules: false,
  devIndicators: false,
  distDir: process.env.GITOWN_NEXT_DIST_DIR || ".next",
  poweredByHeader: false,
  output: "standalone",
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${process.env.GITOWN_API_URL || "http://127.0.0.1:8080"}/api/:path*`,
      },
    ];
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "same-origin" },
          {
            key: "Content-Security-Policy",
            value:
              "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
          },
        ],
      },
    ];
  },
};
export default config;
