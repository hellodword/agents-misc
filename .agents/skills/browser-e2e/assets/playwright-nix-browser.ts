/**
 * Copy this file into project-owned code; do not import it from `.agents/` at runtime.
 * It intentionally uses only Node.js standard-library APIs.
 */
import {
  accessSync,
  constants,
  existsSync,
  readFileSync,
  realpathSync,
  statSync,
  statfsSync,
} from "node:fs";
import { isAbsolute } from "node:path";

export const NIX_BROWSER_PATH_VARIABLE = "PLAYWRIGHT_NIX_BROWSER_PATH";
const CONTAINER_MARKERS = [
  "/.dockerenv",
  "/run/.containerenv",
  "/var/run/.containerenv",
] as const;
const ONE_GIB = 1024n * 1024n * 1024n;

export interface NixBrowserLaunchPolicy {
  headful?: boolean;
}

export interface NixBrowserLaunchOptions {
  executablePath: string;
  headless: boolean;
  args: string[];
}

function isExecutable(path: string): boolean {
  try {
    accessSync(path, constants.X_OK);
    return statSync(path).isFile();
  } catch {
    return false;
  }
}

export function findNixBrowser(
  environment: NodeJS.ProcessEnv = process.env,
): string {
  const configured = environment[NIX_BROWSER_PATH_VARIABLE]?.trim();
  if (!configured) {
    throw new Error(
      `${NIX_BROWSER_PATH_VARIABLE} is unset. Run this test through the project's named Nix development shell; this helper does not search PATH, use a host browser, or download one.`,
    );
  }
  if (!isAbsolute(configured)) {
    throw new Error(
      `${NIX_BROWSER_PATH_VARIABLE} must be an absolute executable path supplied by Nix.`,
    );
  }

  let resolved: string;
  try {
    resolved = realpathSync(configured);
  } catch {
    throw new Error(
      `${NIX_BROWSER_PATH_VARIABLE} does not resolve to an existing path: ${configured}`,
    );
  }
  if (!isExecutable(resolved)) {
    throw new Error(
      `${NIX_BROWSER_PATH_VARIABLE} is not an executable file: ${resolved}`,
    );
  }
  return resolved;
}

export function isContainer(): boolean {
  if (CONTAINER_MARKERS.some((path) => existsSync(path))) return true;
  for (const file of ["/proc/1/cgroup", "/proc/self/cgroup"]) {
    try {
      if (
        /(docker|containerd|kubepods|podman|lxc)/i.test(
          readFileSync(file, "utf8"),
        )
      ) {
        return true;
      }
    } catch {
      // Missing cgroup files are normal outside Linux containers.
    }
  }
  return false;
}

function sharedMemoryBytes(): bigint | undefined {
  try {
    const stats = statfsSync("/dev/shm", { bigint: true });
    return stats.bsize * stats.blocks;
  } catch {
    return undefined;
  }
}

function requireHeadfulDisplay(environment: NodeJS.ProcessEnv): void {
  if (process.platform !== "linux") return;
  if (!environment.DISPLAY && !environment.WAYLAND_DISPLAY) {
    throw new Error(
      "Headful Playwright requires DISPLAY or WAYLAND_DISPLAY on Linux. Configure a display; this helper will not fall back to headless mode.",
    );
  }
}

export function nixBrowserLaunchOptions(
  environment: NodeJS.ProcessEnv = process.env,
  policy: NixBrowserLaunchPolicy = {},
): NixBrowserLaunchOptions {
  const headful = policy.headful === true;
  if (headful) requireHeadfulDisplay(environment);
  const args: string[] = [];
  if (isContainer()) {
    args.push("--no-sandbox");
    const bytes = sharedMemoryBytes();
    if (bytes !== undefined && bytes < ONE_GIB)
      args.push("--disable-dev-shm-usage");
  }
  return {
    executablePath: findNixBrowser(environment),
    headless: !headful,
    args,
  };
}
