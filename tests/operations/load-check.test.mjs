import assert from "node:assert/strict";
import test from "node:test";
import {
  runLoadCheck,
  summarizeLoad,
  validateLoadConfig,
} from "../../scripts/load-check.mjs";

const config = {
  GITOWN_LOAD_TEST_CONFIRM_DISPOSABLE: "true",
  GITOWN_LOAD_TEST_URL: "http://127.0.0.1:8181",
  GITOWN_LOAD_TEST_REQUESTS: "9",
  GITOWN_LOAD_TEST_CONCURRENCY: "3",
};

test("load check requires an explicit disposable HTTPS or loopback target", () => {
  assert.throws(
    () =>
      validateLoadConfig({
        ...config,
        GITOWN_LOAD_TEST_CONFIRM_DISPOSABLE: "false",
      }),
    /disposable test deployment/,
  );
  assert.throws(
    () =>
      validateLoadConfig({
        ...config,
        GITOWN_LOAD_TEST_URL: "http://example.com",
      }),
    /HTTPS origin/,
  );
  assert.throws(
    () =>
      validateLoadConfig({ ...config, GITOWN_LOAD_TEST_CONCURRENCY: "101" }),
    /from 1 to 100/,
  );
});

test("load check reports latency/statuses and fails on errors or latency budget", async () => {
  const endpoints = [];
  let active = 0;
  let maxActive = 0;
  const report = await runLoadCheck(config, {
    fetchImpl: async (url) => {
      endpoints.push(new URL(url).pathname);
      active += 1;
      maxActive = Math.max(maxActive, active);
      await new Promise((resolve) => setTimeout(resolve, 2));
      active -= 1;
      const path = new URL(url).pathname;
      return new Response(
        JSON.stringify(
          path === "/livez"
            ? { status: "alive" }
            : path === "/readyz"
              ? { status: "ok" }
              : { items: [] },
        ),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    },
  });
  assert.equal(report.status, "passed");
  assert.equal(report.request_count, 9);
  assert.ok(maxActive <= 3);
  assert.deepEqual(
    new Set(endpoints),
    new Set(["/livez", "/readyz", "/api/v1/search/repositories"]),
  );
  const twoInstances = validateLoadConfig({
    ...config,
    GITOWN_LOAD_TEST_URLS: "http://127.0.0.1:8181,http://127.0.0.1:8182",
  });
  assert.deepEqual(
    twoInstances.bases.map((base) => base.port),
    ["8181", "8182"],
  );

  const tooSlow = summarizeLoad([{ duration_ms: 15, status: 200 }], 20, {
    ...validateLoadConfig(config),
    p95LimitMs: 10,
  });
  assert.equal(tooSlow.status, "failed");
  const hasFailure = summarizeLoad([{ duration_ms: 1, status: 503 }], 5, {
    ...validateLoadConfig(config),
    p95LimitMs: 10,
  });
  assert.equal(hasFailure.failure_count, 1);
  assert.equal(hasFailure.status, "failed");
});
