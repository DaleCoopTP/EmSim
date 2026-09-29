import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const repositoryRoot = resolve(import.meta.dirname, "../..");

function run(command, args, environment) {
  const result = spawnSync(command, args, {
    cwd: repositoryRoot,
    env: environment,
    stdio: "inherit",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} exited with ${result.status}`);
}

function unusedPort() {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        reject(new Error("could not allocate a local port"));
        return;
      }
      server.close((error) => error ? reject(error) : resolvePort(String(address.port)));
    });
  });
}

const apiPort = process.env.E2E_API_PORT ?? await unusedPort();
const apiAdminPort = await unusedPort();
const workerAdminPort = await unusedPort();
const project = `emsim-e2e-${process.pid}-${Date.now()}`;
// ADR-033: this run's backup copies go to a throwaway directory, never to
// the repository's ./backups.
const backupDir = mkdtempSync(join(tmpdir(), "emsim-e2e-backups-"));
const environment = {
  ...process.env,
  API_PORT: apiPort,
  API_ADMIN_PORT: apiAdminPort,
  WORKER_ADMIN_PORT: workerAdminPort,
  E2E_BASE_URL: `http://127.0.0.1:${apiPort}`,
  PLAYWRIGHT_BROWSERS_PATH: resolve(import.meta.dirname, "../.playwright-browsers"),
  // 112-5a's stub caller (internal/training/operator112/stub_caller.go)
  // defaults to a 1s reply delay so the async protocol is visibly
  // exercised in a manual demo; e2e shortens that to stay fast, without
  // touching the compose default itself. Not zero: operator112.spec.ts's
  // caller-chat test asserts the turn is still "pending" (the "Заявитель
  // печатает…" indicator, held long enough to test hold-cancellation) —
  // a 0s delay lets the worker's reply (poll interval 250ms) land before
  // that state is ever observably rendered.
  CALLER_STUB_DELAY: process.env.CALLER_STUB_DELAY ?? "800ms",
  // ADR-029: the stock stack needs the bundled model. e2e runs without
  // it (compose.no-llm.yaml), always on the deterministic stub caller and
  // with no description judge, whatever the developer's shell exports —
  // the specs assert the stub's fixed phrases and rubric-v2.
  CALLER_REPLIER: "stub",
  ASSESSMENT_JUDGE: "off",
  // ADR-037: no speech model either; the api answers every dictated phrase
  // with a fixed text so the microphone flow is exercised end to end.
  DICTATION: "stub",
  BACKUP_HOST_DIR: backupDir,
  // ADR-038: the stock policy would force every instructor the specs
  // create through the admin API to change the password at first login.
  // Only a newly created admin is forced (the login-policy spec uses that),
  // and three wrong passwords lock an account.
  PASSWORD_FORCE_CHANGE: "admin",
  LOGIN_LOCKOUT_ATTEMPTS: "3",
};
const compose = ["compose", "-p", project, "-f", "compose.yaml", "-f", "compose.no-llm.yaml"];

// The screenshot baselines depend on the browser's font rendering, which
// differs between macOS, a CI runner and every Linux distribution.
// E2E_PLAYWRIGHT=docker runs the specs in Playwright's own linux/amd64
// image, the same one on a developer's machine and in CI, so one set of
// baselines can hold everywhere; the default still uses the host's
// browser until those baselines are regenerated in that image.
const playwrightVersion = JSON.parse(readFileSync(resolve(import.meta.dirname, "../node_modules/@playwright/test/package.json"), "utf8")).version;
const playwrightImage = `mcr.microsoft.com/playwright:v${playwrightVersion}-noble`;
const playwrightInDocker = process.env.E2E_PLAYWRIGHT === "docker";

function runPlaywright(args) {
  if (!playwrightInDocker) {
    run(resolve(import.meta.dirname, "../node_modules/.bin/playwright"), args, environment);
    return;
  }
  // Sharing the api container's network namespace keeps the base URL on
  // 127.0.0.1: a secure context, which the dictation spec's microphone
  // needs. The repository is mounted at its own path so absolute paths in
  // the config (the fake microphone file) resolve unchanged.
  const user = process.platform === "linux" ? ["--user", `${process.getuid()}:${process.getgid()}`] : [];
  const passThrough = ["CI", "CALLER_STUB_DELAY"].filter((name) => process.env[name] !== undefined)
    .flatMap((name) => ["-e", `${name}=${process.env[name]}`]);
  run("docker", [
    "run", "--rm", "--platform", "linux/amd64", "--ipc=host", ...user,
    "--network", `container:${project}-api-1`,
    "-v", `${repositoryRoot}:${repositoryRoot}`, "-w", resolve(repositoryRoot, "web"),
    "-e", "HOME=/tmp", "-e", "E2E_BASE_URL=http://127.0.0.1:8080", ...passThrough,
    playwrightImage, "node", "node_modules/@playwright/test/cli.js", ...args,
  ], environment);
}

try {
  run("docker", [...compose, "up", "--build", "--wait", "--wait-timeout", "180"], environment);
	const playwrightArgs = ["test", "--config", resolve(import.meta.dirname, "../playwright.config.ts")];
	if (process.env.E2E_UPDATE_SNAPSHOTS === "1") playwrightArgs.push("--update-snapshots");
	runPlaywright(playwrightArgs);
} finally {
  // Each run owns its compose project and named volumes. Cleanup therefore
  // cannot touch a developer's ordinary `docker compose up` stack.
  try {
    run("docker", [...compose, "down", "--volumes", "--remove-orphans"], environment);
  } catch (error) {
    console.error("e2e compose cleanup failed:", error);
  }
  // Copies were written by the container's own user (compose chowns the
  // directory to it), so on Linux the runner may not be allowed to remove
  // them; a leftover is only a temp directory, so that is not an error —
  // and it must not replace the tests' own outcome.
  try {
    rmSync(backupDir, { recursive: true, force: true, maxRetries: 2 });
  } catch (error) {
    console.warn(`e2e backup directory ${backupDir} left behind: ${error.code ?? error}`);
  }
}
