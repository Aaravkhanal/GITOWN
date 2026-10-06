import { spawnSync } from "node:child_process";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

function required(env, key) {
  const value = env[key]?.trim();
  if (!value) throw new Error(`${key} is required.`);
  return value;
}

export function validateRestoreDrillConfig(env) {
  if (env.GITOWN_DRILL_CONFIRM_ISOLATED !== "true")
    throw new Error(
      "Set GITOWN_DRILL_CONFIRM_ISOLATED=true only after confirming this is an isolated recovery deployment, never production.",
    );
  let api;
  let gitBase;
  try {
    api = new URL(required(env, "GITOWN_DRILL_API_URL"));
    gitBase = new URL(required(env, "GITOWN_DRILL_GIT_URL"));
  } catch {
    throw new Error("Drill API and Git URLs must be absolute HTTPS URLs.");
  }
  for (const [url, name] of [
    [api, "GITOWN_DRILL_API_URL"],
    [gitBase, "GITOWN_DRILL_GIT_URL"],
  ]) {
    if (
      url.protocol !== "https:" ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      (name === "GITOWN_DRILL_API_URL" && url.pathname !== "/")
    )
      throw new Error(`${name} must be a clean HTTPS URL without credentials.`);
  }
  if (api.hostname !== gitBase.hostname)
    throw new Error(
      "Drill API and Git URLs must use the same host so credentials cannot be sent to a different domain.",
    );
  const owner = required(env, "GITOWN_DRILL_OWNER");
  const repository = required(env, "GITOWN_DRILL_REPOSITORY");
  if (!/^[A-Za-z0-9_.-]+$/.test(owner) || !/^[A-Za-z0-9_.-]+$/.test(repository))
    throw new Error(
      "Drill owner and repository must be simple path components.",
    );
  const issueNumber = Number(required(env, "GITOWN_DRILL_ISSUE_NUMBER"));
  const pullNumber = Number(required(env, "GITOWN_DRILL_PULL_NUMBER"));
  if (!Number.isSafeInteger(issueNumber) || issueNumber < 1)
    throw new Error("GITOWN_DRILL_ISSUE_NUMBER must be a positive integer.");
  if (!Number.isSafeInteger(pullNumber) || pullNumber < 1)
    throw new Error("GITOWN_DRILL_PULL_NUMBER must be a positive integer.");
  const token = required(env, "GITOWN_DRILL_TOKEN");
  if (/[\r\n]/.test(token))
    throw new Error("Drill token contains invalid bytes.");
  const sessionCookie = required(env, "GITOWN_DRILL_SESSION_COOKIE");
  if (!/^[A-Za-z0-9._~-]+$/.test(sessionCookie))
    throw new Error(
      "GITOWN_DRILL_SESSION_COOKIE must be the raw session token value.",
    );
  return {
    api,
    gitBase,
    owner,
    repository,
    issueNumber,
    pullNumber,
    token,
    sessionCookie,
  };
}

async function requestJSON(url, { token, sessionCookie, fetchImpl }) {
  const headers = {};
  if (token) headers.authorization = `Bearer ${token}`;
  if (sessionCookie) headers.cookie = `gitown_session=${sessionCookie}`;
  const response = await fetchImpl(url, {
    headers,
    redirect: "error",
    signal: AbortSignal.timeout(10_000),
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  try {
    return await response.json();
  } catch {
    throw new Error("invalid JSON response");
  }
}

async function writeReport(reportPath, report) {
  if (!reportPath) return;
  if (!reportPath.startsWith("/"))
    throw new Error("GITOWN_DRILL_REPORT must be an absolute path.");
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, {
    flag: "wx",
    mode: 0o600,
  });
}

export async function verifyRestoredDeployment(
  env,
  { fetchImpl = fetch, gitProbe = probeGit } = {},
) {
  const config = validateRestoreDrillConfig(env);
  const start = Date.now();
  const results = [];
  const check = async (name, operation) => {
    const checkStart = Date.now();
    try {
      await operation();
      results.push({
        name,
        status: "passed",
        duration_ms: Date.now() - checkStart,
      });
    } catch (error) {
      results.push({
        name,
        status: "failed",
        duration_ms: Date.now() - checkStart,
      });
      throw new Error(`Restore drill failed at ${name}: ${error.message}`);
    }
  };
  const apiURL = (path) => new URL(path, config.api);
  const repoPath = `/api/v1/repos/${encodeURIComponent(config.owner)}/${encodeURIComponent(config.repository)}`;

  try {
    for (const [path, expected] of [
      ["/livez", "alive"],
      ["/readyz", "ok"],
    ]) {
      await check(path, async () => {
        const response = await fetchImpl(apiURL(path), {
          redirect: "error",
          signal: AbortSignal.timeout(10_000),
        });
        if (
          !response.ok ||
          response.headers.get("x-content-type-options") !== "nosniff"
        )
          throw new Error(`HTTP ${response.status} or missing nosniff header`);
        const body = await response.json();
        if (body.status !== expected)
          throw new Error("unexpected health status");
      });
    }

    await check("private repository is not anonymously visible", async () => {
      const response = await fetchImpl(apiURL(repoPath), {
        redirect: "error",
        signal: AbortSignal.timeout(10_000),
      });
      if (response.status !== 404)
        throw new Error(
          `expected private-repository denial, got HTTP ${response.status}`,
        );
    });

    await check("restored account session", async () => {
      const body = await requestJSON(apiURL("/api/v1/auth/me"), {
        sessionCookie: config.sessionCookie,
        fetchImpl,
      });
      if (!body.user?.username)
        throw new Error("restored session did not resolve to an account");
    });

    await check("token-authenticated repository and Git storage", async () => {
      const body = await requestJSON(apiURL(repoPath), {
        sessionCookie: config.sessionCookie,
        fetchImpl,
      });
      if (body.repository?.visibility !== "private")
        throw new Error(
          "restored repository is not private or repository metadata is missing",
        );
      const remote = new URL(
        `${encodeURIComponent(config.owner)}/${encodeURIComponent(config.repository)}.git`,
        config.gitBase.href.endsWith("/")
          ? config.gitBase
          : `${config.gitBase.href}/`,
      ).href;
      await gitProbe(remote, {
        owner: config.owner,
        token: config.token,
      });
    });

    await check("known issue history", async () => {
      const issue = await requestJSON(
        apiURL(`${repoPath}/issues/${config.issueNumber}`),
        { sessionCookie: config.sessionCookie, fetchImpl },
      );
      if (!issue.number || Number(issue.number) !== config.issueNumber)
        throw new Error("expected restored issue was not found");
    });

    await check("known Unite request history", async () => {
      const pull = await requestJSON(
        apiURL(`${repoPath}/pulls/${config.pullNumber}`),
        { sessionCookie: config.sessionCookie, fetchImpl },
      );
      if (!pull.pull?.number || Number(pull.pull.number) !== config.pullNumber)
        throw new Error("expected restored Unite request was not found");
    });

    const report = {
      format: 1,
      completed_at: new Date().toISOString(),
      duration_ms: Date.now() - start,
      checks: results,
      status: "passed",
    };
    await writeReport(env.GITOWN_DRILL_REPORT, report);
    return report;
  } catch (error) {
    const report = {
      format: 1,
      completed_at: new Date().toISOString(),
      duration_ms: Date.now() - start,
      checks: results,
      status: "failed",
    };
    try {
      await writeReport(env.GITOWN_DRILL_REPORT, report);
    } catch {
      // Keep the original failure; the report path is optional evidence output.
    }
    throw error;
  }
}

async function probeGit(remote, { owner, token }) {
  const directory = await mkdtemp(join(tmpdir(), "gitown-restore-drill-"));
  const askpass = join(directory, "askpass");
  try {
    await writeFile(
      askpass,
      '#!/bin/sh\ncase "$1" in *Username*) printf \'%s\\n\' "$GITOWN_DRILL_OWNER" ;; *Password*) printf \'%s\\n\' "$GITOWN_DRILL_TOKEN" ;; *) exit 1 ;; esac\n',
      { mode: 0o700, flag: "wx" },
    );
    await chmod(askpass, 0o700);
    const result = spawnSync("git", ["ls-remote", remote, "HEAD"], {
      encoding: "utf8",
      timeout: 20_000,
      stdio: ["ignore", "pipe", "ignore"],
      env: {
        ...process.env,
        GIT_ASKPASS: askpass,
        GIT_TERMINAL_PROMPT: "0",
        GITOWN_DRILL_OWNER: owner,
        GITOWN_DRILL_TOKEN: token,
      },
    });
    if (result.error || result.status !== 0 || !result.stdout.trim())
      throw new Error("authenticated git ls-remote failed");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

async function main() {
  const report = await verifyRestoredDeployment(process.env);
  console.log(
    `Isolated restore drill passed ${report.checks.length} checks in ${report.duration_ms}ms.`,
  );
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
