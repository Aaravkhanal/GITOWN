import assert from "node:assert/strict";
import test from "node:test";
import {
  checkDeploymentHealth,
  validateDeploymentConfig,
} from "../../scripts/deployment-check.mjs";

const validConfig = {
  DATABASE_URL: "postgres://gitown:ci-only@db.example:5432/gitown",
  GITOWN_ORIGIN: "https://gitown.example",
  GITOWN_GIT_URL: "https://gitown.example/git",
  GITOWN_SECRET_KEY: "ci-only-not-a-real-secret-with-32-bytes",
  GITOWN_OPERATORS: "release-operator",
  GITOWN_ALLOW_SIGNUP: "false",
};

test("deployment preflight accepts hardened HTTPS configuration", () => {
  const result = validateDeploymentConfig(validConfig);
  assert.deepEqual(result, { errors: [], warnings: [] });
});

test("deployment preflight rejects weak keys, HTTP origins, and missing operators", () => {
  const result = validateDeploymentConfig({
    ...validConfig,
    GITOWN_SECRET_KEY: "too-short",
    GITOWN_ORIGIN: "http://gitown.example",
    GITOWN_OPERATORS: " , ",
  });
  assert.equal(result.errors.length, 3);
  assert.ok(result.errors.some((error) => error.includes("32 bytes")));
  assert.ok(result.errors.some((error) => error.includes("GITOWN_ORIGIN")));
  assert.ok(result.errors.some((error) => error.includes("GITOWN_OPERATORS")));
  assert.ok(!result.errors.some((error) => error.includes("too-short")));
});

test("verified email and trusted proxy flags require their deployment dependencies", () => {
  const result = validateDeploymentConfig({
    ...validConfig,
    GITOWN_REQUIRE_VERIFIED_EMAIL: "true",
    GITOWN_BEHIND_PROXY: "true",
  });
  assert.ok(result.errors.some((error) => error.includes("SMTP_ADDR")));
  assert.ok(result.errors.some((error) => error.includes("SMTP_FROM")));
  assert.ok(result.errors.some((error) => error.includes("TRUSTED_PROXIES")));
});

test("public beta requires an independent review record", () => {
  const blocked = validateDeploymentConfig({
    ...validConfig,
    GITOWN_PUBLIC_BETA: "true",
  });
  assert.ok(
    blocked.errors.some((error) =>
      error.includes("completed independent review"),
    ),
  );
  const approved = validateDeploymentConfig({
    ...validConfig,
    GITOWN_PUBLIC_BETA: "true",
    GITOWN_INDEPENDENT_SECURITY_REVIEW: "https://reviews.example/report/123",
  });
  assert.deepEqual(approved.errors, []);
});

test("health check requires live, ready, JSON, and baseline security headers", async () => {
  const requested = [];
  await checkDeploymentHealth("https://gitown.example", async (url) => {
    requested.push(new URL(url).pathname);
    const status = new URL(url).pathname === "/livez" ? "alive" : "ok";
    return new Response(JSON.stringify({ status }), {
      status: 200,
      headers: {
        "content-type": "application/json",
        "x-content-type-options": "nosniff",
      },
    });
  });
  assert.deepEqual(requested, ["/livez", "/readyz"]);

  await assert.rejects(
    checkDeploymentHealth("http://192.0.2.1", async () => new Response("{}")),
    /require HTTPS/,
  );
  await assert.rejects(
    checkDeploymentHealth(
      "https://gitown.example",
      async () =>
        new Response(JSON.stringify({ status: "alive" }), { status: 503 }),
    ),
    /HTTP 503/,
  );
});
