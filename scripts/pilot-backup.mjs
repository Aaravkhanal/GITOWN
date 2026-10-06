import { spawnSync } from "node:child_process";
import { access } from "node:fs/promises";
import { isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const composePrefix = [
  "compose",
  "--env-file",
  ".env.pilot",
  "-f",
  "compose.yaml",
  "-f",
  "compose.pilot.yaml",
];

function command(binary, args, options = {}) {
  const result = spawnSync(binary, args, {
    encoding: "utf8",
    stdio: options.inherit ? "inherit" : "pipe",
    env: options.env || process.env,
  });
  if (result.error || result.status !== 0)
    throw new Error(`${binary} command failed. Existing data was not removed.`);
  return result.stdout || "";
}

export function resolvePilotBackupTargets(config) {
  const api = config?.services?.api;
  const postgres = config?.services?.postgres;
  const dbURL = api?.environment?.DATABASE_URL;
  const repoMount = api?.volumes?.find(
    (volume) =>
      volume.target === "/data/repositories" && volume.type === "bind",
  );
  const dbPort = postgres?.ports?.find(
    (port) =>
      Number(port.target) === 5432 &&
      String(port.host_ip || "") === "127.0.0.1" &&
      Number(port.published) > 0,
  );
  if (!dbURL || !repoMount?.source || !isAbsolute(repoMount.source) || !dbPort)
    throw new Error(
      "Pilot backup requires an absolute repository bind mount and a loopback-only PostgreSQL port.",
    );
  const database = new URL(dbURL);
  if (!database.username || !database.password || !database.pathname)
    throw new Error("Resolved pilot database URL is incomplete.");
  database.hostname = "127.0.0.1";
  database.port = String(dbPort.published);
  return { databaseURL: database.toString(), repositoryPath: repoMount.source };
}

export async function runOfflinePilotBackup({
  snapshot,
  config,
  runCompose,
  runBackup,
}) {
  if (!snapshot || !isAbsolute(snapshot))
    throw new Error("Choose a new absolute path for the backup snapshot.");
  const targets = resolvePilotBackupTargets(config);
  const snapshotPath = resolve(snapshot);
  const storagePath = resolve(targets.repositoryPath);
  if (
    snapshotPath === storagePath ||
    snapshotPath.startsWith(storagePath + "/") ||
    storagePath.startsWith(snapshotPath + "/")
  )
    throw new Error(
      "Backup snapshot and repository storage must be separate paths.",
    );
  await access(targets.repositoryPath);
  let stopAttempted = false;
  let primaryError;
  try {
    stopAttempted = true;
    await runCompose(["stop", "caddy", "web", "api"]);
    await runBackup({
      snapshot: snapshotPath,
      databaseURL: targets.databaseURL,
      repositoryPath: targets.repositoryPath,
    });
  } catch (error) {
    primaryError = error;
  } finally {
    if (stopAttempted) {
      try {
        await runCompose(["up", "-d", "api", "web", "caddy"]);
      } catch (restartError) {
        if (!primaryError)
          primaryError = new Error(
            `Backup finished or failed, and the pilot stack could not be restarted: ${restartError.message}`,
          );
        else
          primaryError = new Error(
            `${primaryError.message} The pilot stack also could not be restarted: ${restartError.message}`,
          );
      }
    }
  }
  if (primaryError) throw primaryError;
  return targets;
}

async function main(args) {
  if (args.length !== 1)
    throw new Error(
      "Usage: npm run pilot:backup -- /absolute/new-snapshot-directory",
    );
  const snapshot = resolve(args[0]);
  if (!isAbsolute(args[0]))
    throw new Error("Choose a new absolute path for the backup snapshot.");
  const config = JSON.parse(
    command("docker", [...composePrefix, "config", "--format", "json"]),
  );
  const runCompose = async (argsToCompose) =>
    command("docker", [...composePrefix, ...argsToCompose]);
  const runBackup = async ({
    databaseURL,
    repositoryPath,
    snapshot: target,
  }) => {
    command("node", ["scripts/backup.mjs", "create", target], {
      inherit: true,
      env: {
        ...process.env,
        GITOWN_OFFLINE: "true",
        DATABASE_URL: databaseURL,
        GITOWN_DATA_DIR: repositoryPath,
      },
    });
  };
  await runOfflinePilotBackup({ snapshot, config, runCompose, runBackup });
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
