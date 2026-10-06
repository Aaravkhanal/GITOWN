import { isIP } from "node:net";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const TRUE = new Set(["1", "true", "yes"]);

function enabled(value) {
  return TRUE.has(
    String(value || "")
      .trim()
      .toLowerCase(),
  );
}

function parseWebURL(value, label, { origin = false } = {}) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(`${label} must be an absolute HTTPS URL.`);
  }
  if (
    parsed.protocol !== "https:" ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash ||
    (origin && parsed.pathname !== "/")
  ) {
    throw new Error(
      `${label} must be a clean HTTPS URL without credentials, query, or fragment.`,
    );
  }
  return parsed;
}

function validProxyEntry(value) {
  const [address, prefix, ...extra] = value.split("/");
  const version = isIP(address);
  if (!version || extra.length) return false;
  if (prefix === undefined) return true;
  if (!/^\d+$/.test(prefix)) return false;
  const bits = Number(prefix);
  return bits >= 0 && bits <= (version === 4 ? 32 : 128);
}

export function validateDeploymentConfig(env) {
  const errors = [];
  if (!env.DATABASE_URL) errors.push("DATABASE_URL is required.");
  else {
    try {
      const db = new URL(env.DATABASE_URL);
      if (!["postgres:", "postgresql:"].includes(db.protocol) || !db.hostname)
        errors.push("DATABASE_URL must identify a PostgreSQL server.");
    } catch {
      errors.push("DATABASE_URL must be a valid PostgreSQL URL.");
    }
  }
  for (const [key, label, opts] of [
    ["GITOWN_ORIGIN", "GITOWN_ORIGIN", { origin: true }],
    ["GITOWN_GIT_URL", "GITOWN_GIT_URL", {}],
  ]) {
    try {
      parseWebURL(env[key], label, opts);
    } catch (error) {
      errors.push(error.message);
    }
  }
  if (Buffer.byteLength(env.GITOWN_SECRET_KEY || "", "utf8") < 32)
    errors.push(
      "GITOWN_SECRET_KEY must contain at least 32 bytes and remain stable across restarts.",
    );
  const operators = (env.GITOWN_OPERATORS || "")
    .split(",")
    .map((name) => name.trim())
    .filter(Boolean);
  if (!operators.length)
    errors.push("GITOWN_OPERATORS must name at least one operator account.");

  if (enabled(env.GITOWN_REQUIRE_VERIFIED_EMAIL)) {
    const [host, port, ...extra] = (env.GITOWN_SMTP_ADDR || "").split(":");
    if (
      !host ||
      extra.length ||
      !/^\d+$/.test(port || "") ||
      Number(port) < 1 ||
      Number(port) > 65535
    )
      errors.push(
        "Verified-email enforcement requires GITOWN_SMTP_ADDR in host:port form.",
      );
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(env.GITOWN_SMTP_FROM || ""))
      errors.push(
        "Verified-email enforcement requires a valid GITOWN_SMTP_FROM.",
      );
  }

  if (enabled(env.GITOWN_BEHIND_PROXY)) {
    const proxies = (env.GITOWN_TRUSTED_PROXIES || "")
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
    if (!proxies.length || proxies.some((entry) => !validProxyEntry(entry)))
      errors.push(
        "GITOWN_BEHIND_PROXY requires exact IP/CIDR entries in GITOWN_TRUSTED_PROXIES.",
      );
  }

  if (enabled(env.GITOWN_PUBLIC_BETA)) {
    let review;
    try {
      review = new URL(env.GITOWN_INDEPENDENT_SECURITY_REVIEW || "");
    } catch {
      review = null;
    }
    if (review?.protocol !== "https:" || !review.hostname)
      errors.push(
        "Public beta is blocked until GITOWN_INDEPENDENT_SECURITY_REVIEW points to the completed independent review record.",
      );
  }

  return {
    errors,
    warnings: enabled(env.GITOWN_ALLOW_SIGNUP)
      ? [
          "Account signup is enabled; confirm that this is intended for the deployment.",
        ]
      : [],
  };
}

export async function checkDeploymentHealth(baseURL, fetchImpl = fetch) {
  let base;
  try {
    base = new URL(baseURL);
  } catch {
    throw new Error("GITOWN_DEPLOY_URL must be an absolute HTTPS URL.");
  }
  const localHTTP =
    base.protocol === "http:" &&
    ["localhost", "127.0.0.1", "::1"].includes(base.hostname);
  if (base.protocol !== "https:" && !localHTTP)
    throw new Error(
      "Deployment health checks require HTTPS (HTTP is allowed only for loopback smoke tests).",
    );

  for (const path of ["/livez", "/readyz"]) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 5000);
    let response;
    try {
      response = await fetchImpl(new URL(path, base), {
        signal: controller.signal,
        redirect: "error",
      });
    } catch {
      throw new Error(
        `${path} could not be reached; check deployment networking and TLS.`,
      );
    } finally {
      clearTimeout(timer);
    }
    if (!response.ok)
      throw new Error(`${path} returned HTTP ${response.status}.`);
    if (response.headers.get("x-content-type-options") !== "nosniff")
      throw new Error(
        `${path} is missing the X-Content-Type-Options: nosniff response header.`,
      );
    let body;
    try {
      body = await response.json();
    } catch {
      throw new Error(`${path} did not return JSON.`);
    }
    const expected = path === "/livez" ? "alive" : "ok";
    if (body.status !== expected)
      throw new Error(`${path} returned an unexpected status.`);
  }
}

async function main(args) {
  const mode = args[0];
  if (!mode || args.length !== 1 || !["--config", "--health"].includes(mode))
    throw new Error(
      "Usage: node scripts/deployment-check.mjs --config|--health (health requires GITOWN_DEPLOY_URL)",
    );

  if (mode === "--config") {
    const result = validateDeploymentConfig(process.env);
    if (result.errors.length) throw new Error(result.errors.join("\n"));
    for (const warning of result.warnings) console.warn(`WARNING: ${warning}`);
    console.log(
      "Deployment configuration checks passed (secret values were not printed).",
    );
    return;
  }

  await checkDeploymentHealth(process.env.GITOWN_DEPLOY_URL);
  console.log(
    "Deployment liveness, readiness, and baseline response-header checks passed.",
  );
}

if (
  process.argv[1] &&
  fileURLToPath(import.meta.url) === resolve(process.argv[1])
) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
