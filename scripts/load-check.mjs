import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

function numberSetting(env, name, fallback, min, max) {
  const raw = env[name];
  if (raw === undefined || raw === "") return fallback;
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value < min || value > max)
    throw new Error(`${name} must be an integer from ${min} to ${max}.`);
  return value;
}

export function validateLoadConfig(env) {
  if (env.GITOWN_LOAD_TEST_CONFIRM_DISPOSABLE !== "true")
    throw new Error(
      "Set GITOWN_LOAD_TEST_CONFIRM_DISPOSABLE=true only for a disposable test deployment.",
    );
  const rawTargets =
    env.GITOWN_LOAD_TEST_URLS || env.GITOWN_LOAD_TEST_URL || "";
  let bases;
  try {
    bases = rawTargets.split(",").map((value) => new URL(value.trim()));
  } catch {
    throw new Error("GITOWN_LOAD_TEST_URL must be an absolute HTTPS URL.");
  }
  if (!bases.length || bases.some((base) => !base.hostname))
    throw new Error("At least one load-test origin is required.");
  for (const base of bases) {
    const loopbackHTTP =
      base.protocol === "http:" &&
      ["localhost", "127.0.0.1", "[::1]"].includes(base.hostname);
    if (
      (base.protocol !== "https:" && !loopbackHTTP) ||
      base.username ||
      base.password ||
      base.search ||
      base.hash ||
      base.pathname !== "/"
    )
      throw new Error(
        "Load checks require HTTPS origins (HTTP is allowed only for loopback) without credentials, query, or path.",
      );
  }
  return {
    bases,
    requests: numberSetting(env, "GITOWN_LOAD_TEST_REQUESTS", 100, 1, 10_000),
    concurrency: numberSetting(env, "GITOWN_LOAD_TEST_CONCURRENCY", 10, 1, 100),
    timeoutMs: numberSetting(
      env,
      "GITOWN_LOAD_TEST_TIMEOUT_MS",
      10_000,
      100,
      60_000,
    ),
    p95LimitMs: numberSetting(
      env,
      "GITOWN_LOAD_TEST_P95_LIMIT_MS",
      5_000,
      1,
      60_000,
    ),
  };
}

export function summarizeLoad(samples, elapsedMs, config) {
  const latencies = samples
    .map((sample) => sample.duration_ms)
    .sort((a, b) => a - b);
  const percentile = (fraction) =>
    latencies.length
      ? latencies[
          Math.min(
            latencies.length - 1,
            Math.ceil(latencies.length * fraction) - 1,
          )
        ]
      : 0;
  const statusCounts = {};
  for (const sample of samples) {
    const key = sample.status === null ? "error" : String(sample.status);
    statusCounts[key] = (statusCounts[key] || 0) + 1;
  }
  const failed = samples.filter((sample) => sample.status !== 200).length;
  return {
    format: 1,
    completed_at: new Date().toISOString(),
    target_hosts: config.bases.map((base) => base.host),
    request_count: samples.length,
    concurrency: config.concurrency,
    elapsed_ms: elapsedMs,
    requests_per_second: elapsedMs
      ? Number(((samples.length * 1000) / elapsedMs).toFixed(2))
      : 0,
    latency_ms: {
      p50: percentile(0.5),
      p95: percentile(0.95),
      p99: percentile(0.99),
      max: latencies.at(-1) || 0,
    },
    status_counts: statusCounts,
    failure_count: failed,
    status:
      failed === 0 && percentile(0.95) <= config.p95LimitMs
        ? "passed"
        : "failed",
  };
}

export async function runLoadCheck(env, { fetchImpl = fetch } = {}) {
  const config = validateLoadConfig(env);
  const paths = [
    "/livez",
    "/readyz",
    "/api/v1/search/repositories?sort=recent&offset=0",
  ];
  const samples = new Array(config.requests);
  let next = 0;
  const start = Date.now();
  async function worker() {
    for (;;) {
      const index = next++;
      if (index >= config.requests) return;
      const requestStart = Date.now();
      let status = null;
      try {
        const pathIndex = index % paths.length;
        const targetIndex =
          Math.floor(index / paths.length) % config.bases.length;
        const response = await fetchImpl(
          new URL(paths[pathIndex], config.bases[targetIndex]),
          {
            method: "GET",
            redirect: "error",
            signal: AbortSignal.timeout(config.timeoutMs),
          },
        );
        status = response.status;
        if (status === 200 && pathIndex === 2) {
          const body = await response.json();
          if (!Array.isArray(body.items)) status = 502;
        } else if (status === 200) {
          const body = await response.json();
          const expected = pathIndex === 0 ? "alive" : "ok";
          if (body.status !== expected) status = 502;
        }
      } catch {
        // Keep reports free of exception text, which can contain deployment URLs.
        status = null;
      }
      samples[index] = { duration_ms: Date.now() - requestStart, status };
    }
  }
  await Promise.all(
    Array.from(
      { length: Math.min(config.concurrency, config.requests) },
      worker,
    ),
  );
  const report = summarizeLoad(samples, Date.now() - start, config);
  if (env.GITOWN_LOAD_TEST_REPORT) {
    const reportPath = resolve(env.GITOWN_LOAD_TEST_REPORT);
    await mkdir(dirname(reportPath), { recursive: true });
    await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, {
      flag: "wx",
      mode: 0o600,
    });
  }
  return report;
}

async function main() {
  const report = await runLoadCheck(process.env);
  console.log(JSON.stringify(report, null, 2));
  if (report.status !== "passed") process.exitCode = 1;
}

if (
  process.argv[1] &&
  fileURLToPath(import.meta.url) === resolve(process.argv[1])
) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
