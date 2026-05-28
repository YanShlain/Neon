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

export async function startNeonServer(opts: StartServerOptions): Promise<NeonServer> {
  const baseURL = `http://127.0.0.1:${opts.port}`;
  const child = spawn(
    "go",
    ["run", "./cmd/api"],
    {
      cwd: process.cwd(),
      env: {
        ...process.env,
        API_ADDR: `:${opts.port}`,
        TEMPORAL_AUTO_DEV: "1",
        HOLD_DURATION: "2m",
        ...opts.env,
      },
      stdio: "pipe",
      shell: process.platform === "win32",
    },
  );

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
      break;
    }
    try {
      const response = await client.get(`${baseURL}/api/v1/flights`);
      if (response.ok()) {
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
  throw new Error(`Neon server did not become ready at ${baseURL}. ${lastError}`);
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
