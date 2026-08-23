import { execFile, spawn, type ChildProcess } from "node:child_process";
import { constants } from "node:fs";
import {
  access,
  mkdtemp,
  mkdir,
  readFile,
  realpath,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import {
  chromium,
  test as base,
  type Browser,
  type BrowserContext,
  type Page,
} from "@playwright/test";
import { nixBrowserLaunchOptions } from "./browser";

type Runtime = {
  baseURL: string;
  browser: Browser;
  cacheDir: string;
  context: BrowserContext;
  page: Page;
  process: ChildProcess;
  rollout: string;
  sourceHome: string;
};

type Options = {
  password: string;
};

const here = dirname(fileURLToPath(import.meta.url));
const fixturePath = resolve(here, "../../tests/fixtures/rollouts/v0_120.jsonl");
const viewerRoot = resolve(here, "../..");
const repositoryRoot = resolve(viewerRoot, "../..");
const execFileAsync = promisify(execFile);

async function resolveViewerBinary(
  environment: NodeJS.ProcessEnv = process.env,
): Promise<string> {
  const configured = environment.AGENTS_VIEWER_E2E_BINARY?.trim();
  if (configured) {
    if (!isAbsolute(configured)) {
      throw new Error(
        "AGENTS_VIEWER_E2E_BINARY must be an absolute executable path.",
      );
    }
    let binary: string;
    try {
      binary = await realpath(configured);
      const metadata = await stat(binary);
      await access(binary, constants.X_OK);
      if (!metadata.isFile()) throw new Error("not a file");
    } catch {
      throw new Error(
        `AGENTS_VIEWER_E2E_BINARY is not an executable file: ${configured}`,
      );
    }
    return binary;
  }

  const metadata = JSON.parse(
    (
      await execFileAsync(
        "cargo",
        [
          "metadata",
          "--format-version",
          "1",
          "--no-deps",
          "--manifest-path",
          resolve(viewerRoot, "Cargo.toml"),
        ],
        { cwd: repositoryRoot },
      )
    ).stdout,
  ) as { target_directory: string };
  return resolve(metadata.target_directory, "debug/agents-viewer");
}

async function stopServer(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolveExit) => {
    let forceTimer: NodeJS.Timeout | undefined;
    const finish = () => {
      if (forceTimer) clearTimeout(forceTimer);
      resolveExit();
    };
    child.once("close", finish);
    forceTimer = setTimeout(() => child.kill("SIGKILL"), 5_000);
    if (!child.kill("SIGTERM")) {
      child.off("close", finish);
      finish();
      return;
    }
  });
}

async function startServer(
  sourceHome: string,
  dataDir: string,
  password: string,
) {
  const binary = await resolveViewerBinary();
  const configPath = resolve(sourceHome, "../config.toml");
  await writeFile(
    configPath,
    `source_dir = ${JSON.stringify(sourceHome)}\ndata_dir = ${JSON.stringify(dataDir)}\ninitial_index_days = -1\nlisten = "127.0.0.1:0"\npassword = ${JSON.stringify(password)}\nmax_event_bytes = "32MiB"\nlog_level = "warn"\n`,
    { mode: 0o600 },
  );
  const child = spawn(binary, ["--config", configPath], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  const stderr: Buffer[] = [];
  child.stderr?.on("data", (chunk) => stderr.push(Buffer.from(chunk)));
  try {
    const baseURL = await new Promise<string>((resolveURL, reject) => {
      const timer = setTimeout(
        () =>
          reject(
            new Error(
              `agents-viewer did not print its URL; stderr: ${Buffer.concat(stderr).toString("utf8")}`,
            ),
          ),
        10_000,
      );
      child.once("error", (error) => {
        clearTimeout(timer);
        reject(error);
      });
      child.once("exit", (code) => {
        clearTimeout(timer);
        reject(
          new Error(
            `agents-viewer exited with ${code}; stderr: ${Buffer.concat(stderr).toString("utf8")}`,
          ),
        );
      });
      child.stdout?.once("data", (chunk) => {
        clearTimeout(timer);
        const url = String(chunk)
          .trim()
          .split(/\s+/)
          .find((value) => value.startsWith("http://"));
        url
          ? resolveURL(url)
          : reject(new Error(`agents-viewer printed no URL: ${String(chunk)}`));
      });
    });
    return { child, baseURL };
  } catch (error) {
    await stopServer(child);
    throw error;
  }
}

export const test = base.extend<Runtime & Options>({
  password: ["", { option: true }],
  browser: [
    async ({}, use) => {
      const browser = await chromium.launch(nixBrowserLaunchOptions());
      try {
        await use(browser);
      } finally {
        await browser.close();
      }
    },
    { scope: "worker" },
  ],
  sourceHome: async ({}, use) => {
    const root = await mkdtemp(resolve(tmpdir(), "agents-viewer-e2e-"));
    const sourceHome = resolve(root, "source");
    await mkdir(resolve(sourceHome, "sessions/2025/01/02"), {
      recursive: true,
    });
    try {
      await use(sourceHome);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  },
  cacheDir: async ({ sourceHome }, use) => {
    const cacheDir = resolve(sourceHome, "../cache");
    await use(cacheDir);
  },
  rollout: async ({ sourceHome }, use) => {
    const rollout = resolve(
      sourceHome,
      "sessions/2025/01/02/rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl",
    );
    const base = await readFile(fixturePath, "utf8");
    const pagination = Array.from({ length: 110 }, (_, index) =>
      JSON.stringify({
        timestamp: `2025-01-02T03:${String(5 + Math.floor(index / 60)).padStart(2, "0")}:${String(index % 60).padStart(2, "0")}.000Z`,
        type: "event_msg",
        payload: {
          type: index % 2 === 0 ? "user_message" : "agent_message",
          message: `Pagination message ${index}`,
          phase: index % 2 === 0 ? undefined : "final",
        },
      }),
    ).join("\n");
    const markdown = JSON.stringify({
      timestamp: "2025-01-02T03:07:00.000Z",
      type: "event_msg",
      payload: {
        type: "agent_message",
        message:
          "Markdown copy fixture: `viewerInline`\n\n```typescript\nconst viewerValue: number = 42;\nconsole.log(viewerValue);\n```",
        phase: "final",
      },
    });
    await writeFile(
      resolve(
        sourceHome,
        "sessions/2025/01/02/rollout-2025-01-01T00-00-00-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.jsonl",
      ),
      [
        {
          timestamp: "2025-01-01T00:00:00Z",
          type: "session_meta",
          payload: {
            id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            cwd: "/work/plan",
            source: "cli",
          },
        },
        {
          timestamp: "2025-01-01T00:00:30Z",
          type: "response_item",
          payload: {
            type: "message",
            role: "user",
            content: [{ type: "input_text", text: "Plan session grouping" }],
          },
        },
        {
          timestamp: "2025-01-01T00:01:00Z",
          type: "response_item",
          payload: {
            type: "message",
            role: "assistant",
            content: [
              {
                type: "output_text",
                text: "<proposed_plan>\n# Group sessions\nImplement the tree\n</proposed_plan>",
              },
            ],
          },
        },
      ]
        .map((record) => JSON.stringify(record))
        .join("\n") + "\n",
    );
    await writeFile(
      resolve(
        sourceHome,
        "sessions/2025/01/02/rollout-2025-01-01T00-02-00-bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.jsonl",
      ),
      [
        {
          timestamp: "2025-01-01T00:02:00Z",
          type: "session_meta",
          payload: {
            id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
            cwd: "/work/plan",
            source: "exec",
          },
        },
        {
          timestamp: "2025-01-01T00:02:30Z",
          type: "response_item",
          payload: {
            type: "message",
            role: "user",
            content: [
              {
                type: "input_text",
                text: "A previous agent produced the plan below to accomplish the user's task. Implement the plan in a fresh context. Treat the plan as the source of user intent, re-read files as needed, and carry the work through implementation and verification.\n\n# Group sessions\nImplement the tree",
              },
            ],
          },
        },
      ]
        .map((record) => JSON.stringify(record))
        .join("\n") + "\n",
    );
    await writeFile(rollout, `${base.trimEnd()}\n${pagination}\n${markdown}\n`);
    await use(rollout);
  },
  process: async (
    { sourceHome, cacheDir, rollout: _rollout, password },
    use,
  ) => {
    const server = await startServer(sourceHome, cacheDir, password);
    Object.assign(server.child, { baseURL: server.baseURL });
    try {
      await use(server.child);
    } finally {
      await stopServer(server.child);
    }
  },
  baseURL: async ({ process: child }, use) => {
    await use((child as ChildProcess & { baseURL: string }).baseURL);
  },
  context: async ({ browser, baseURL, password }, use) => {
    const context = await browser.newContext({
      baseURL,
      locale: "en-US",
      viewport: { width: 1440, height: 900 },
      permissions: ["clipboard-read", "clipboard-write"],
      httpCredentials: password
        ? { username: "agents-viewer", password }
        : undefined,
    });
    await context.route("**/*", (route) => {
      const url = new URL(route.request().url());
      if (
        (url.protocol === "http:" || url.protocol === "https:") &&
        !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)
      )
        return route.abort("blockedbyclient");
      return route.continue();
    });
    try {
      await use(context);
    } finally {
      await context.close();
    }
  },
  page: async ({ context, baseURL }, use) => {
    const page = await context.newPage();
    await page.goto(baseURL);
    await use(page);
  },
});

export { expect } from "@playwright/test";
