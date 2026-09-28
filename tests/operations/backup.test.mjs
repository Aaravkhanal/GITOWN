import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";

const databaseUrl = process.env.TEST_DATABASE_URL;

function withDbName(name) {
  const url = new URL(databaseUrl);
  url.pathname = "/" + name;
  return url.toString();
}

function databaseName(prefix) {
  const suffix = `${Date.now().toString(36)}_${process.pid}`;
  return `${prefix}_${suffix}`.replaceAll("-", "_");
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(
      `${command} ${args.join(" ")} failed\n${result.stdout}\n${result.stderr}`,
    );
  }
  return result.stdout;
}

function tryRun(command, args, options = {}) {
  return spawnSync(command, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    ...options,
  });
}

test(
  "offline backup create and restore preserves metadata and bare repositories",
  { skip: !databaseUrl && "TEST_DATABASE_URL is required" },
  async () => {
    const sourceDb = databaseName("gitown_backup_source");
    const restoreDb = databaseName("gitown_backup_restore");
    const sourceUrl = withDbName(sourceDb);
    const restoreUrl = withDbName(restoreDb);
    const workspace = mkdtempSync(join(tmpdir(), "gitown-backup-test-"));
    const sourceStorage = join(workspace, "source-repos");
    const restoreStorage = join(workspace, "restored-repos");
    const snapshot = join(workspace, "snapshot");

    run("createdb", ["--maintenance-db=" + databaseUrl, sourceDb]);
    run("createdb", ["--maintenance-db=" + databaseUrl, restoreDb]);
    await mkdir(sourceStorage);

    try {
      run("psql", [
        sourceUrl,
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        "CREATE TABLE repositories (id text PRIMARY KEY, name text NOT NULL); INSERT INTO repositories VALUES ('repo-1','proof');",
      ]);

      const repo = join(workspace, "work");
      run("git", ["init", "-b", "main", repo]);
      writeFileSync(join(repo, "README.md"), "# Backup proof\n");
      run("git", ["-C", repo, "add", "README.md"]);
      run("git", [
        "-C",
        repo,
        "-c",
        "user.name=GITOWN Test",
        "-c",
        "user.email=test@gitown.local",
        "commit",
        "-m",
        "Add proof",
      ]);
      run("git", ["clone", "--bare", repo, join(sourceStorage, "repo-1.git")]);

      run("node", ["scripts/backup.mjs", "create", snapshot], {
        env: {
          ...process.env,
          DATABASE_URL: sourceUrl,
          GITOWN_DATA_DIR: sourceStorage,
          GITOWN_OFFLINE: "true",
        },
      });

      const repeatCreate = tryRun(
        "node",
        ["scripts/backup.mjs", "create", snapshot],
        {
          env: {
            ...process.env,
            DATABASE_URL: sourceUrl,
            GITOWN_DATA_DIR: sourceStorage,
            GITOWN_OFFLINE: "true",
          },
        },
      );
      assert.notEqual(repeatCreate.status, 0);
      assert.match(repeatCreate.stderr, /never overwritten/);

      run("node", ["scripts/backup.mjs", "restore", snapshot], {
        env: {
          ...process.env,
          DATABASE_URL: restoreUrl,
          GITOWN_DATA_DIR: restoreStorage,
          GITOWN_OFFLINE: "true",
        },
      });

      const rows = run("psql", [
        restoreUrl,
        "-v",
        "ON_ERROR_STOP=1",
        "-tAc",
        "SELECT name FROM repositories WHERE id = 'repo-1'",
      ]).trim();
      assert.equal(rows, "proof");

      const readme = run("git", [
        "--git-dir=" + join(restoreStorage, "repo-1.git"),
        "show",
        "main:README.md",
      ]);
      assert.equal(readme, "# Backup proof\n");

      const repeatRestore = tryRun(
        "node",
        ["scripts/backup.mjs", "restore", snapshot],
        {
          env: {
            ...process.env,
            DATABASE_URL: restoreUrl,
            GITOWN_DATA_DIR: join(workspace, "second-restore"),
            GITOWN_OFFLINE: "true",
          },
        },
      );
      assert.notEqual(repeatRestore.status, 0);
      assert.match(repeatRestore.stderr, /empty dedicated database/);
    } finally {
      tryRun("dropdb", [
        "--if-exists",
        "--maintenance-db=" + databaseUrl,
        sourceDb,
      ]);
      tryRun("dropdb", [
        "--if-exists",
        "--maintenance-db=" + databaseUrl,
        restoreDb,
      ]);
      await rm(workspace, { recursive: true, force: true });
    }
  },
);
