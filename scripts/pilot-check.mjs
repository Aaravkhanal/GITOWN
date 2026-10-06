import { spawnSync } from "node:child_process";
import { stat, readFile } from "node:fs/promises";
import { basename, isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseEnv } from "node:util";
import { isIP } from "node:net";
import { validateDeploymentConfig } from "./deployment-check.mjs";

const IMAGE_KEYS = [
  ["PILOT_POSTGRES_IMAGE", "postgres:"],
  ["PILOT_CADDY_IMAGE", "caddy:"],
  ["PILOT_PROMETHEUS_IMAGE", "prom/prometheus:"],
  ["PILOT_GRAFANA_IMAGE", "grafana/grafana:"],
];
function isIPv4InCIDR(address, cidr) {
  const [network, prefixText] = String(cidr).split("/");
  if (isIP(address) !== 4 || isIP(network) !== 4) return false;
  const prefix = Number(prefixText);
  if (!Number.isInteger(prefix) || prefix < 0 || prefix > 32) return false;
  const toInt = (ip) =>
    ip.split(".").reduce((value, octet) => (value << 8n) | BigInt(octet), 0n);
  const mask =
    prefix === 0 ? 0n : ((1n << BigInt(prefix)) - 1n) << BigInt(32 - prefix);
  return (toInt(address) & mask) === (toInt(network) & mask);
}

function privateIPv4(address) {
  if (isIP(address) !== 4) return false;
  return [
    "10.0.0.0/8",
    "172.16.0.0/12",
    "192.168.0.0/16",
    "100.64.0.0/10",
  ].some((range) => isIPv4InCIDR(address, range));
}

function looksLikePlaceholder(value) {
  return /replace|placeholder|example\.(com|net|org|test)|\.example$|change-this|0000000000/i.test(
    String(value || ""),
  );
}

export function validatePilotSettings(env, { onboardingWindow = false } = {}) {
  const errors = [];
  const signup = String(env.GITOWN_ALLOW_SIGNUP || "false").toLowerCase();
  if (signup !== "false" && signup !== "0") {
    if (!onboardingWindow || env.PILOT_ONBOARDING_ACL_CONFIRMED !== "true")
      errors.push(
        "Signup must stay disabled; onboarding windows require --onboarding-window and PILOT_ONBOARDING_ACL_CONFIRMED=true.",
      );
  }
  if (String(env.GITOWN_REQUIRE_VERIFIED_EMAIL).toLowerCase() !== "true")
    errors.push("The private pilot must require verified email.");
  for (const key of ["POSTGRES_PASSWORD", "GITOWN_SECRET_KEY"])
    if (
      !/^[a-f0-9]{64}$/i.test(env[key] || "") ||
      looksLikePlaceholder(env[key])
    )
      errors.push(
        `${key} must be a generated 32-byte secret encoded as 64 hex characters.`,
      );
  if (env.POSTGRES_PASSWORD && env.POSTGRES_PASSWORD === env.GITOWN_SECRET_KEY)
    errors.push(
      "POSTGRES_PASSWORD and GITOWN_SECRET_KEY must be different secrets.",
    );
  if (
    !/^[a-f0-9]{64}$/i.test(env.GRAFANA_ADMIN_PASSWORD || "") ||
    looksLikePlaceholder(env.GRAFANA_ADMIN_PASSWORD)
  )
    errors.push(
      "GRAFANA_ADMIN_PASSWORD must be a separate generated 32-byte secret.",
    );
  if (
    looksLikePlaceholder(env.GITOWN_SMTP_USER) ||
    looksLikePlaceholder(env.GITOWN_SMTP_PASSWORD)
  )
    errors.push(
      "Replace the SMTP credential placeholders or leave both SMTP credentials empty if the relay is unauthenticated.",
    );
  if (Boolean(env.GITOWN_SMTP_USER) !== Boolean(env.GITOWN_SMTP_PASSWORD))
    errors.push(
      "Set both GITOWN_SMTP_USER and GITOWN_SMTP_PASSWORD, or leave both empty.",
    );

  if (!privateIPv4(env.PILOT_BIND_IP))
    errors.push(
      "PILOT_BIND_IP must be a private/VPN IPv4 address, never a public address or 0.0.0.0.",
    );
  if (
    isIP(env.PILOT_PROXY_IP) !== 4 ||
    !isIPv4InCIDR(env.PILOT_PROXY_IP, env.PILOT_DOCKER_SUBNET)
  )
    errors.push(
      "PILOT_PROXY_IP must be an IPv4 address inside PILOT_DOCKER_SUBNET.",
    );
  const trusted = (env.GITOWN_TRUSTED_PROXIES || "")
    .split(",")
    .map((value) => value.trim());
  if (!trusted.includes(`${env.PILOT_PROXY_IP}/32`))
    errors.push(
      "GITOWN_TRUSTED_PROXIES must include only the exact Caddy proxy address as /32.",
    );
  if (
    !isAbsolute(env.GITOWN_REPOSITORY_PATH || "") ||
    resolve(env.GITOWN_REPOSITORY_PATH || "/") === "/" ||
    basename(env.GITOWN_REPOSITORY_PATH || "") !== "repositories"
  )
    errors.push(
      "GITOWN_REPOSITORY_PATH must be a dedicated absolute host directory named repositories.",
    );
  if (
    !isAbsolute(env.PILOT_TLS_DIR || "") ||
    resolve(env.PILOT_TLS_DIR || "/") === "/"
  )
    errors.push("PILOT_TLS_DIR must be an absolute host path.");
  if (
    looksLikePlaceholder(env.GITOWN_DOMAIN) ||
    looksLikePlaceholder(env.GITOWN_OPERATORS)
  )
    errors.push(
      "Replace the pilot DNS name and operator username placeholders.",
    );
  if (env.GITOWN_DOMAIN && env.GITOWN_ORIGIN) {
    try {
      if (new URL(env.GITOWN_ORIGIN).hostname !== env.GITOWN_DOMAIN)
        errors.push("GITOWN_DOMAIN must match the host in GITOWN_ORIGIN.");
    } catch {
      // The deployment validator below reports malformed URLs.
    }
  }
  if (env.GITOWN_GIT_URL && env.GITOWN_ORIGIN) {
    try {
      const gitURL = new URL(env.GITOWN_GIT_URL);
      const origin = new URL(env.GITOWN_ORIGIN);
      if (gitURL.hostname !== origin.hostname || gitURL.pathname !== "/git")
        errors.push("GITOWN_GIT_URL must use the pilot host and end in /git.");
    } catch {
      // The deployment validator below reports malformed URLs.
    }
  }
  for (const [key, officialPrefix] of IMAGE_KEYS)
    if (
      !String(env[key] || "").startsWith(officialPrefix) ||
      !/^[^\s@]+@sha256:[a-f0-9]{64}$/i.test(env[key] || "") ||
      looksLikePlaceholder(env[key])
    )
      errors.push(
        `${key} must be an official image pinned to a real SHA-256 digest.`,
      );

  const deployment = validateDeploymentConfig({
    ...env,
    GITOWN_BEHIND_PROXY: "true",
    DATABASE_URL: env.DATABASE_URL || "",
  });
  errors.push(...deployment.errors);
  return errors;
}

function composeConfig({ quiet = true, envFile = ".env.pilot" } = {}) {
  const args = [
    "compose",
    "--env-file",
    envFile,
    "-f",
    "compose.yaml",
    "-f",
    "compose.pilot.yaml",
    "config",
    ...(quiet ? ["--quiet"] : ["--format", "json"]),
  ];
  const result = spawnSync("docker", args, { encoding: "utf8", stdio: "pipe" });
  if (result.error || result.status !== 0)
    throw new Error(
      "Docker Compose validation failed; review the pilot files without printing secret-bearing config.",
    );
  return quiet ? null : JSON.parse(result.stdout);
}

async function main(args) {
  if (args.length === 1 && args[0] === "--structure-only") {
    composeConfig({ envFile: ".env.pilot.example" });
    console.log(
      "Private-pilot Compose structure is valid; example secrets are not deployable.",
    );
    return;
  }
  if (
    args.length > 1 ||
    (args.length === 1 && args[0] !== "--onboarding-window")
  )
    throw new Error(
      "Usage: node scripts/pilot-check.mjs [--onboarding-window|--structure-only]",
    );

  const envPath = resolve(".env.pilot");
  const mode = (await stat(envPath)).mode;
  if ((mode & 0o077) !== 0)
    throw new Error(
      ".env.pilot must be private to its owner (chmod 600); no secret values were printed.",
    );
  const env = parseEnv(await readFile(envPath, "utf8"));
  if (args[0] === "--onboarding-window")
    env.PILOT_ONBOARDING_ACL_CONFIRMED =
      process.env.PILOT_ONBOARDING_ACL_CONFIRMED;
  const config = composeConfig({ quiet: false });
  const resolvedEnv = {
    ...env,
    DATABASE_URL: config.services?.api?.environment?.DATABASE_URL,
  };
  const errors = validatePilotSettings(resolvedEnv, {
    onboardingWindow: args[0] === "--onboarding-window",
  });

  const tlsDirectory = env.PILOT_TLS_DIR;
  for (const filename of ["fullchain.pem", "privkey.pem"]) {
    try {
      const info = await stat(resolve(tlsDirectory, filename));
      if (!info.isFile())
        errors.push(`${filename} must be a regular certificate file.`);
      if (filename === "privkey.pem" && (info.mode & 0o077) !== 0)
        errors.push("TLS private key permissions must be 600 or stricter.");
    } catch {
      errors.push(`TLS certificate file is missing: ${filename}.`);
    }
  }
  if (errors.length) throw new Error(errors.join("\n"));

  composeConfig();
  console.log(
    args[0] === "--onboarding-window"
      ? "Pilot configuration is valid for a restricted onboarding window. Confirm VPN/firewall allowlisting before starting."
      : "Private-pilot configuration is valid (secret values were not printed).",
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
