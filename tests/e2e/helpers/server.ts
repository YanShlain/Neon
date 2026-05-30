import { request } from "@playwright/test";
import { ChildProcess, spawn } from "child_process";

type StartServerOptions = {
  port: number;
  env?: Record<string, string>;
};

export type NeonServer = {
  baseURL: string;
  stop: () => Promise<void>;
};

function buildServerEnv(opts: StartServerOptions): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env };
  env.API_ADDR = `:${opts.port}`;
  env.TEMPORAL_AUTO_DEV = "1";
  for (const [key, value] of Object.entries(opts.env ?? {})) {
    env[key] = value;
  }
  if (!env.HOLD_DURATION) {
    env.HOLD_DURATION = "2m";
  }
  return env;
}

export async function startNeonServer(opts: StartServerOptions): Promise<NeonServer> {
  const baseURL = `http://127.0.0.1:${opts.port}`;
  const goCmd = process.platform === "win32" ? "go.exe" : "go";
  const child = spawn(goCmd, ["run", "./cmd/api"], {
    cwd: process.cwd(),
    env: buildServerEnv(opts),
    stdio: "pipe",
    shell: false,
  });

  await waitForServerReady(baseURL, child);

  return {
    baseURL,
    stop: async () => {
      await stopChild(child);
    },
  };
}

async function waitForServerReady(baseURL: string, child: ChildProcess): Promise<void> {
  const client = await request.newContext();
  const deadline = Date.now() + 60000;
  let lastError = "";

  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      lastError = `process exited with code ${child.exitCode}`;
      break;
    }
    try {
      const response = await client.get(`${baseURL}/api/v1/flights`);
      if (response.ok()) {
        if (child.exitCode !== null) {
          lastError = `process exited with code ${child.exitCode}`;
          break;
        }
        await client.dispose();
        return;
      }
      lastError = `status=${response.status()}`;
    } catch (err) {
      lastError = String(err);
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }

  await client.dispose();
  throw new Error(
    `Neon server did not become ready at ${baseURL}. ${lastError}. ` +
      "If the port is already in use, stop the old process or use a different port.",
  );
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) {
    return;
  }

  child.kill("SIGTERM");
  await Promise.race([
    new Promise<void>((resolve) => {
      child.once("exit", () => resolve());
    }),
    new Promise<void>((resolve) => setTimeout(resolve, 5000)),
  ]);

  if (child.exitCode === null) {
    child.kill("SIGKILL");
  }
}
