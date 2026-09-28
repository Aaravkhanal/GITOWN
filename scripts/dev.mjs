// Start a private development database, API, and web app without touching other projects.
import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import net from "node:net";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
process.chdir(root);
const localGo = join(root, ".tools/go/bin/go");
const go = existsSync(localGo) ? localGo : "go";
const data = join(root, ".data");
mkdirSync(data, { recursive: true, mode: 0o700 });
mkdirSync(join(root, ".tools"), { recursive: true });
const env = { ...process.env, GOCACHE: join(root, ".tools/go-cache") };
const processes = [];
let stopping = false;
function stop(code = 0) {
  if (stopping) return;
  stopping = true;
  process.exitCode = code;
  for (const child of processes.reverse()) child.kill("SIGTERM");
  setTimeout(() => process.exit(code), 800).unref();
}
process.on("SIGINT", () => stop());
process.on("SIGTERM", () => stop());
function run(command, args) {
  const result = spawnSync(command, args, { cwd: root, env, stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(`${command} failed (${result.status}).`);
}
function start(command, args) {
  const child = spawn(command, args, { cwd: root, env, stdio: "inherit" });
  processes.push(child);
  child.on("error", (error) => {
    console.error(error.message);
    stop(1);
  });
  child.on("exit", (code) => {
    if (!stopping) {
      console.error(`${command} exited (${code}).`);
      stop(code || 1);
    }
  });
  return child;
}
async function freePort(port) {
  await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", (error) =>
      reject(
        new Error(
          error.code === "EADDRINUSE"
            ? `Port ${port} is already in use. Choose different GITOWN ports.`
            : `Cannot bind port ${port}: ${error.message}`,
        ),
      ),
    );
    server.listen(port, "127.0.0.1", () => server.close(resolve));
  });
}
async function waitFor(check, description) {
  for (let i = 0; i < 120; i++) {
    if (stopping) throw new Error("Startup interrupted.");
    if (await check()) return;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`${description} did not become ready.`);
}

try {
  const webPort = Number(process.env.GITOWN_WEB_PORT || 3000);
  const apiPort = Number(process.env.GITOWN_API_PORT || 8080);
  await freePort(webPort);
  await freePort(apiPort);
  if (!env.DATABASE_URL) {
    const pgPort = Number(process.env.GITOWN_PG_PORT || 55432);
    await freePort(pgPort);
    const cluster = join(data, "postgres");
    if (!existsSync(join(cluster, "PG_VERSION")))
      run("initdb", [
        "-D",
        cluster,
        "-U",
        "gitown",
        "-A",
        "trust",
        "--encoding=UTF8",
        "--locale=C",
      ]);
    console.log(
      "GITOWN's development database uses local trust authentication on loopback only.",
    );
    start("postgres", [
      "-D",
      cluster,
      "-h",
      "127.0.0.1",
      "-p",
      String(pgPort),
      "-k",
      data,
    ]);
    await waitFor(
      () =>
        spawnSync(
          "pg_isready",
          [
            "-h",
            "127.0.0.1",
            "-p",
            String(pgPort),
            "-U",
            "gitown",
            "-d",
            "postgres",
          ],
          { stdio: "ignore" },
        ).status === 0,
      "PostgreSQL",
    );
    const admin = `postgres://gitown@127.0.0.1:${pgPort}/postgres?sslmode=disable`;
    const found = spawnSync(
      "psql",
      [admin, "-tAc", "SELECT 1 FROM pg_database WHERE datname='gitown'"],
      { encoding: "utf8" },
    );
    if (found.status !== 0)
      throw new Error("Could not inspect the development database.");
    if (found.stdout.trim() !== "1")
      run("createdb", [
        "-h",
        "127.0.0.1",
        "-p",
        String(pgPort),
        "-U",
        "gitown",
        "gitown",
      ]);
    env.DATABASE_URL = `postgres://gitown@127.0.0.1:${pgPort}/gitown?sslmode=disable`;
  }
  Object.assign(env, {
    GITOWN_ADDR: `127.0.0.1:${apiPort}`,
    GITOWN_ORIGIN: `http://localhost:${webPort}`,
    GITOWN_GIT_URL: `http://localhost:${apiPort}/git`,
    GITOWN_DATA_DIR: process.env.GITOWN_DATA_DIR || join(data, "repositories"),
    GITOWN_ALLOW_SIGNUP: process.env.GITOWN_ALLOW_SIGNUP || "true",
    GITOWN_API_URL: `http://127.0.0.1:${apiPort}`,
  });
  run(go, ["build", "-o", ".tools/gitown", "./apps/server"]);
  start(join(root, ".tools/gitown"), []);
  await waitFor(async () => {
    try {
      return (await fetch(`http://127.0.0.1:${apiPort}/healthz`)).ok;
    } catch {
      return false;
    }
  }, "GITOWN API");
  start(process.execPath, [
    "node_modules/next/dist/bin/next",
    "dev",
    "apps/web",
    "--hostname",
    "127.0.0.1",
    "--port",
    String(webPort),
  ]);
  console.log(
    `\nGITOWN → http://localhost:${webPort}\nGit endpoint → http://localhost:${apiPort}/git\nCtrl+C stops this project's services.\n`,
  );
} catch (error) {
  console.error(
    `\nGITOWN startup: ${error.message}\nRequires Node 22+, Go 1.26+, Git 2.39+, and PostgreSQL 15+ binaries in PATH (or DATABASE_URL).`,
  );
  stop(1);
}
