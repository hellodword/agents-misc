import {
  accessSync,
  constants,
  existsSync,
  readFileSync,
  realpathSync,
  statfsSync,
  statSync,
} from "node:fs";
import { isAbsolute } from "node:path";

export const NIX_BROWSER_PATH_VARIABLE = "PLAYWRIGHT_NIX_BROWSER_PATH";

const CONTAINER_MARKERS = [
  "/.dockerenv",
  "/run/.containerenv",
  "/var/run/.containerenv",
] as const;
const ONE_GIB = 1024n * 1024n * 1024n;

export type NixBrowserLaunchOptions = {
  executablePath: string;
  headless: boolean;
  args: string[];
};

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
      `${NIX_BROWSER_PATH_VARIABLE} is unset. Run E2E through the agents-viewer Nix shell; the test does not search PATH, connect to another browser, or download one.`,
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
    return BigInt(stats.bsize) * BigInt(stats.blocks);
  } catch {
    return undefined;
  }
}

export function nixBrowserLaunchOptions(
  environment: NodeJS.ProcessEnv = process.env,
): NixBrowserLaunchOptions {
  const args: string[] = [];
  if (isContainer()) {
    args.push("--no-sandbox");
    const bytes = sharedMemoryBytes();
    if (bytes !== undefined && bytes < ONE_GIB) {
      args.push("--disable-dev-shm-usage");
    }
  }
  return {
    executablePath: findNixBrowser(environment),
    headless: true,
    args,
  };
}
