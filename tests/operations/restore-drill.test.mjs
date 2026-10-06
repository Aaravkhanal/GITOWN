import assert from "node:assert/strict";
import test from "node:test";
import {
  validateRestoreDrillConfig,
  verifyRestoredDeployment,
} from "../../scripts/restore-drill.mjs";

const config = {
  GITOWN_DRILL_CONFIRM_ISOLATED: "true",
  GITOWN_DRILL_API_URL: "https://restore.example",
  GITOWN_DRILL_GIT_URL: "https://restore.example/git",
  GITOWN_DRILL_OWNER: "test-owner",
  GITOWN_DRILL_REPOSITORY: "restore-proof",
  GITOWN_DRILL_ISSUE_NUMBER: "7",
  GITOWN_DRILL_PULL_NUMBER: "3",
  GITOWN_DRILL_TOKEN: "test-token-never-reported",
  GITOWN_DRILL_SESSION_COOKIE: "test-session-never-reported",
};

test("restore drill requires explicit isolation and complete verification inputs", () => {
  assert.throws(
    () =>
      validateRestoreDrillConfig({
        ...config,
        GITOWN_DRILL_CONFIRM_ISOLATED: "false",
      }),
    /isolated recovery deployment/,
  );
  assert.throws(
    () =>
      validateRestoreDrillConfig({
        ...config,
        GITOWN_DRILL_API_URL: "http://restore.example",
      }),
    /HTTPS/,
  );
  assert.throws(
    () =>
      validateRestoreDrillConfig({
        ...config,
        GITOWN_DRILL_GIT_URL: "https://other.example/git",
      }),
    /same host/,
  );
  assert.throws(
    () =>
      validateRestoreDrillConfig({
        ...config,
        GITOWN_DRILL_REPOSITORY: "../outside",
      }),
    /path components/,
  );
});

test("restored deployment verifier checks health, private access, session, Git, issue, and Unite history", async () => {
  const paths = [];
  let gitRemote = "";
  const report = await verifyRestoredDeployment(config, {
    fetchImpl: async (url, options = {}) => {
      const path = new URL(url).pathname;
      paths.push(path);
      if (
        path === "/api/v1/repos/test-owner/restore-proof" &&
        !options.headers?.cookie
      )
        return new Response("{}", { status: 404 });
      if (path === "/livez" || path === "/readyz") {
        return new Response(
          JSON.stringify({ status: path === "/livez" ? "alive" : "ok" }),
          {
            status: 200,
            headers: {
              "content-type": "application/json",
              "x-content-type-options": "nosniff",
            },
          },
        );
      }
      if (path === "/api/v1/auth/me")
        return new Response(
          JSON.stringify({ user: { username: "drill-user" } }),
        );
      if (path === "/api/v1/repos/test-owner/restore-proof")
        return new Response(
          JSON.stringify({ repository: { visibility: "private" } }),
        );
      if (path.endsWith("/issues/7"))
        return new Response(
          JSON.stringify({ number: 7, title: "Known issue" }),
        );
      if (path.endsWith("/pulls/3"))
        return new Response(
          JSON.stringify({ pull: { number: 3, title: "Known Unite" } }),
        );
      return new Response("{}", { status: 404 });
    },
    gitProbe: async (remote, credentials) => {
      gitRemote = remote;
      assert.equal(credentials.owner, "test-owner");
      assert.equal(credentials.token, config.GITOWN_DRILL_TOKEN);
    },
  });

  assert.equal(report.status, "passed");
  assert.equal(report.checks.length, 7);
  assert.ok(report.checks.every((check) => check.status === "passed"));
  assert.ok(paths.includes("/api/v1/auth/me"));
  assert.equal(
    gitRemote,
    "https://restore.example/git/test-owner/restore-proof.git",
  );
  assert.equal(
    JSON.stringify(report).includes(config.GITOWN_DRILL_TOKEN),
    false,
  );
  assert.equal(
    JSON.stringify(report).includes(config.GITOWN_DRILL_SESSION_COOKIE),
    false,
  );
});

test("restore drill fails closed when private repository becomes anonymously visible", async () => {
  await assert.rejects(
    verifyRestoredDeployment(config, {
      fetchImpl: async (url) => {
        const path = new URL(url).pathname;
        if (path === "/livez" || path === "/readyz")
          return new Response(
            JSON.stringify({ status: path === "/livez" ? "alive" : "ok" }),
            {
              status: 200,
              headers: {
                "content-type": "application/json",
                "x-content-type-options": "nosniff",
              },
            },
          );
        return new Response(
          JSON.stringify({ repository: { visibility: "private" } }),
          { status: 200 },
        );
      },
      gitProbe: async () =>
        assert.fail(
          "Git must not be probed after failed anonymous privacy check",
        ),
    }),
    /private repository is not anonymously visible/,
  );
});
