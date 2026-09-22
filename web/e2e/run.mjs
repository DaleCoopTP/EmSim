import { createServer } from "node:net";
import { resolve } from "node:path";
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
const environment = {
  ...process.env,
  API_PORT: apiPort,
  API_ADMIN_PORT: apiAdminPort,
  WORKER_ADMIN_PORT: workerAdminPort,
  E2E_BASE_URL: `http://127.0.0.1:${apiPort}`,
  PLAYWRIGHT_BROWSERS_PATH: resolve(import.meta.dirname, "../.playwright-browsers"),
};
const compose = ["compose", "-p", project];

try {
  run("docker", [...compose, "up", "--build", "--wait", "--wait-timeout", "180"], environment);
	const playwrightArgs = ["test", "--config", resolve(import.meta.dirname, "../playwright.config.ts")];
	if (process.env.E2E_UPDATE_SNAPSHOTS === "1") playwrightArgs.push("--update-snapshots");
	run(resolve(import.meta.dirname, "../node_modules/.bin/playwright"), playwrightArgs, environment);
} finally {
  // Each run owns its compose project and named volumes. Cleanup therefore
  // cannot touch a developer's ordinary `docker compose up` stack.
  try {
    run("docker", [...compose, "down", "--volumes", "--remove-orphans"], environment);
  } catch (error) {
    console.error("e2e compose cleanup failed:", error);
  }
}
