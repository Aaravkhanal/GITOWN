import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
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

    try {
      run("createdb", ["--maintenance-db=" + databaseUrl, sourceDb]);
      run("createdb", ["--maintenance-db=" + databaseUrl, restoreDb]);
      await mkdir(sourceStorage);
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

test("restore refuses a corrupt snapshot before touching recovery targets", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "gitown-backup-corrupt-test-"));
  const snapshot = join(workspace, "snapshot");
  const storage = join(workspace, "recovered-repos");
  await mkdir(snapshot);
  writeFileSync(join(snapshot, "metadata.dump"), "corrupted database dump");
  writeFileSync(
    join(snapshot, "manifest.json"),
    JSON.stringify({ format: 1, metadata_sha256: "0".repeat(64) }),
  );

  try {
    const restore = tryRun(
      "node",
      ["scripts/backup.mjs", "restore", snapshot],
      {
        env: {
          ...process.env,
          DATABASE_URL: "postgres://unused:unused@127.0.0.1:5432/unused",
          GITOWN_DATA_DIR: storage,
          GITOWN_OFFLINE: "true",
        },
      },
    );
    assert.notEqual(restore.status, 0);
    assert.match(restore.stderr, /checksum does not match/);
    assert.equal(existsSync(storage), false);
    assert.equal(
      readFileSync(join(snapshot, "metadata.dump"), "utf8"),
      "corrupted database dump",
    );
  } finally {
    await rm(workspace, { recursive: true, force: true });
  }
});
