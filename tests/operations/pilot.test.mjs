import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  resolvePilotBackupTargets,
  runOfflinePilotBackup,
} from "../../scripts/pilot-backup.mjs";
import { validatePilotSettings } from "../../scripts/pilot-check.mjs";

function validPilotEnv() {
  return {
    POSTGRES_PASSWORD: "a".repeat(64),
    GITOWN_SECRET_KEY: "b".repeat(64),
    GITOWN_ALLOW_SIGNUP: "false",
    GITOWN_REQUIRE_VERIFIED_EMAIL: "true",
    GITOWN_OPERATORS: "pilotoperator",
    GITOWN_ORIGIN: "https://gitown.private.test",
    GITOWN_GIT_URL: "https://gitown.private.test/git",
    GITOWN_DOMAIN: "gitown.private.test",
    GITOWN_TRUSTED_PROXIES: "172.29.242.2/32",
    GITOWN_SMTP_ADDR: "smtp.private.test:587",
    GITOWN_SMTP_FROM: "noreply@private.test",
    GITOWN_SMTP_USER: "mailer",
    GITOWN_SMTP_PASSWORD: "mail-secret",
    PILOT_BIND_IP: "100.100.10.20",
    PILOT_DOCKER_SUBNET: "172.29.242.0/24",
    PILOT_PROXY_IP: "172.29.242.2",
    PILOT_TLS_DIR: "/srv/gitown/tls",
    GITOWN_REPOSITORY_PATH: "/srv/gitown/repositories",
    PILOT_POSTGRES_IMAGE: `postgres:17-alpine@sha256:${"a".repeat(64)}`,
    PILOT_CADDY_IMAGE: `caddy:2-alpine@sha256:${"b".repeat(64)}`,
    PILOT_PROMETHEUS_IMAGE: `prom/prometheus:v3@sha256:${"c".repeat(64)}`,
    PILOT_GRAFANA_IMAGE: `grafana/grafana:12@sha256:${"d".repeat(64)}`,
    GRAFANA_ADMIN_USER: "pilotadmin",
    GRAFANA_ADMIN_PASSWORD: "e".repeat(64),
    DATABASE_URL:
      "postgres://gitown:secret@postgres:5432/gitown?sslmode=disable",
  };
}

test("pilot preflight rejects public binds, enabled signup, weak secrets, and unpinned images", () => {
  const env = validPilotEnv();
  env.PILOT_BIND_IP = "8.8.8.8";
  env.GITOWN_ALLOW_SIGNUP = "true";
  env.GITOWN_SECRET_KEY = "too-short";
  env.PILOT_CADDY_IMAGE = "caddy:latest";
  const errors = validatePilotSettings(env);
  assert.ok(errors.some((error) => error.includes("private/VPN")));
  assert.ok(
    errors.some((error) => error.includes("Signup must stay disabled")),
  );
  assert.ok(errors.some((error) => error.includes("GITOWN_SECRET_KEY")));
  assert.ok(errors.some((error) => error.includes("PILOT_CADDY_IMAGE")));
  assert.ok(
    !errors.some((error) =>
      error.includes("secret" + " " + env.GITOWN_SECRET_KEY),
    ),
  );
  env.GITOWN_REPOSITORY_PATH = "/";
  assert.ok(
    validatePilotSettings(env).some((error) =>
      error.includes("dedicated absolute host directory"),
    ),
  );
});

test("pilot onboarding requires explicit acknowledgement of private network ACLs", () => {
  const env = validPilotEnv();
  env.GITOWN_ALLOW_SIGNUP = "true";
  assert.ok(
    validatePilotSettings(env, { onboardingWindow: true }).some((error) =>
      error.includes("PILOT_ONBOARDING_ACL_CONFIRMED"),
    ),
  );
  env.PILOT_ONBOARDING_ACL_CONFIRMED = "true";
  assert.deepEqual(validatePilotSettings(env, { onboardingWindow: true }), []);
});

test("pilot backup only derives host targets from loopback database and bind-mounted Git storage", () => {
  const config = {
    services: {
      api: {
        environment: {
          DATABASE_URL:
            "postgres://gitown:secret@postgres:5432/gitown?sslmode=disable",
        },
        volumes: [
          {
            type: "bind",
            source: "/srv/gitown/repositories",
            target: "/data/repositories",
          },
        ],
      },
      postgres: {
        ports: [{ target: 5432, published: "55432", host_ip: "127.0.0.1" }],
      },
    },
  };
  const targets = resolvePilotBackupTargets(config);
  assert.equal(
    targets.databaseURL,
    "postgres://gitown:secret@127.0.0.1:55432/gitown?sslmode=disable",
  );
  assert.equal(targets.repositoryPath, "/srv/gitown/repositories");
  assert.throws(
    () =>
      resolvePilotBackupTargets({
        ...config,
        services: {
          ...config.services,
          postgres: {
            ports: [{ target: 5432, published: "55432", host_ip: "0.0.0.0" }],
          },
        },
      }),
    /loopback-only/,
  );
});

test("pilot backup restarts services even if backup creation fails", async () => {
  const temp = await mkdtemp(join(tmpdir(), "gitown-pilot-backup-"));
  const repositoryPath = join(temp, "repositories");
  await mkdir(repositoryPath);
  const config = {
    services: {
      api: {
        environment: {
          DATABASE_URL:
            "postgres://gitown:secret@postgres:5432/gitown?sslmode=disable",
        },
        volumes: [
          {
            type: "bind",
            source: repositoryPath,
            target: "/data/repositories",
          },
        ],
      },
      postgres: {
        ports: [{ target: 5432, published: "55432", host_ip: "127.0.0.1" }],
      },
    },
  };
  const calls = [];
  try {
    await assert.rejects(
      runOfflinePilotBackup({
        snapshot: join(temp, "snapshot"),
        config,
        runCompose: async (args) => calls.push(args),
        runBackup: async () => {
          throw new Error("synthetic backup failure");
        },
      }),
      /synthetic backup failure/,
    );
    assert.deepEqual(calls, [
      ["stop", "caddy", "web", "api"],
      ["up", "-d", "api", "web", "caddy"],
    ]);
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});
