// Offline snapshots only. The API and any other Git writers must be stopped.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import {
  access,
  chmod,
  cp,
  mkdir,
  readFile,
  readdir,
  writeFile,
} from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";

async function exists(path) {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}
async function digest(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}
function run(command, args) {
  const result = spawnSync(command, args, {
    stdio: ["ignore", "pipe", "pipe"],
    encoding: "utf8",
  });
  if (result.error || result.status !== 0)
    throw new Error(
      `${command} failed. Check tool availability, database access, and the output directory. No existing data was deleted.`,
    );
  return result.stdout;
}

async function main() {
  const [mode, snapshotArg] = process.argv.slice(2);
  if (
    !["create", "restore"].includes(mode) ||
    !snapshotArg ||
    !isAbsolute(snapshotArg)
  )
    throw new Error(
      "Usage: node scripts/backup.mjs create|restore /absolute/snapshot-directory",
    );
  if (process.env.GITOWN_OFFLINE !== "true")
    throw new Error(
      "Stop the API and all Git writers, then explicitly set GITOWN_OFFLINE=true. Online snapshots are not supported.",
    );
  const database = process.env.DATABASE_URL;
  const storageArg = process.env.GITOWN_DATA_DIR;
  if (!database || !storageArg)
    throw new Error("DATABASE_URL and GITOWN_DATA_DIR are required.");
  const snapshot = resolve(snapshotArg);
  const storage = resolve(storageArg);
  if (
    snapshot === storage ||
    snapshot.startsWith(storage + "/") ||
    storage.startsWith(snapshot + "/")
  )
    throw new Error(
      "Snapshot and repository storage must be separate directories.",
    );
  if (mode === "create") {
    if (await exists(snapshot))
      throw new Error(
        "Use a new snapshot directory. Existing snapshots are never overwritten.",
      );
    await access(storage);
    await mkdir(snapshot, { recursive: false, mode: 0o700 });
    const dump = join(snapshot, "metadata.dump");
    run("pg_dump", [
      "--dbname=" + database,
      "--format=custom",
      "--no-owner",
      "--file=" + dump,
    ]);
    await chmod(dump, 0o600);
    await cp(storage, join(snapshot, "repositories"), {
      recursive: true,
      errorOnExist: true,
      force: false,
    });
    const manifest = {
      format: 1,
      created_at: new Date().toISOString(),
      metadata_sha256: await digest(dump),
    };
    // Written last: absence of manifest marks an incomplete snapshot.
    await writeFile(
      join(snapshot, "manifest.json"),
      JSON.stringify(manifest, null, 2) + "\n",
      { flag: "wx", mode: 0o600 },
    );
    console.log(
      `Offline backup created at ${snapshot}. Encrypt it before off-host storage.`,
    );
  } else {
    const manifest = JSON.parse(
      await readFile(join(snapshot, "manifest.json"), "utf8"),
    );
    if (
      manifest.format !== 1 ||
      manifest.metadata_sha256 !==
        (await digest(join(snapshot, "metadata.dump")))
    )
      throw new Error("Snapshot metadata checksum does not match.");
    if (await exists(storage))
      throw new Error(
        "Restore requires a new repository directory. Existing storage is never overwritten.",
      );
    const count = run("psql", [
      database,
      "-v",
      "ON_ERROR_STOP=1",
      "-tAc",
      "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')",
    ]).trim();
    if (count !== "0")
      throw new Error(
        "Restore requires an empty dedicated database. Existing tables are never overwritten.",
      );
    await mkdir(storage, { recursive: false, mode: 0o700 });
    await cp(join(snapshot, "repositories"), storage, {
      recursive: true,
      errorOnExist: true,
      force: false,
    });
    for (const entry of await readdir(storage, { withFileTypes: true })) {
      if (entry.isDirectory() && entry.name.endsWith(".git"))
        run("git", [
          "--git-dir=" + join(storage, entry.name),
          "fsck",
          "--full",
        ]);
    }
    run("pg_restore", [
      "--dbname=" + database,
      "--no-owner",
      "--no-privileges",
      "--exit-on-error",
      join(snapshot, "metadata.dump"),
    ]);
    console.log(
      `Restored metadata and repositories to ${storage}. Start a separate API instance and verify clone, login, and PR history before switching traffic.`,
    );
  }
}
main().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
