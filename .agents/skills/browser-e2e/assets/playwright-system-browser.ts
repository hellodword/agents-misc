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
  statfsSync,
} from "node:fs";
import { delimiter, extname, join, normalize } from "node:path";

const BROWSER_NAMES = ["google-chrome", "chromium", "microsoft-edge"] as const;
const CONTAINER_MARKERS = [
  "/.dockerenv",
  "/run/.containerenv",
  "/var/run/.containerenv",
] as const;
const ONE_GIB = 1024n * 1024n * 1024n;

export interface SystemBrowserLaunchPolicy {
  headful?: boolean;
}

export interface SystemBrowserLaunchOptions {
  executablePath: string;
  headless: boolean;
  args: string[];
}

function unquotePathEntry(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    return trimmed.slice(1, -1);
  }
  return trimmed;
}

function environmentValue(
  environment: NodeJS.ProcessEnv,
  name: string,
): string | undefined {
  const matchingKey = Object.keys(environment).find(
    (key) => key.toUpperCase() === name,
  );
  return matchingKey === undefined ? undefined : environment[matchingKey];
}

function pathEntries(environment: NodeJS.ProcessEnv): string[] {
  const raw = environmentValue(environment, "PATH");
  if (!raw) throw new Error("Cannot select a browser: PATH is empty or unset.");

  const seen = new Set<string>();
  const entries: string[] = [];
  for (const rawEntry of raw.split(delimiter)) {
    const entry = unquotePathEntry(rawEntry);
    if (!entry) continue;
    const key =
      process.platform === "win32"
        ? normalize(entry).toLowerCase()
        : normalize(entry);
    if (!seen.has(key)) {
      seen.add(key);
      entries.push(entry);
    }
  }
  if (entries.length === 0) {
    throw new Error(
      "Cannot select a browser: PATH contains no usable directories.",
    );
  }
  return entries;
}

function executableSuffixes(environment: NodeJS.ProcessEnv): string[] {
  if (process.platform !== "win32") return [""];
  const values = (
    environmentValue(environment, "PATHEXT") || ".COM;.EXE;.BAT;.CMD"
  )
    .split(";")
    .map((value) => value.trim())
    .filter(Boolean);
  return ["", ...new Set(values.map((value) => value.toLowerCase()))];
}

function isExecutable(path: string): boolean {
  try {
    const mode = process.platform === "win32" ? constants.F_OK : constants.X_OK;
    accessSync(path, mode);
    return true;
  } catch {
    return false;
  }
}

export function findSystemBrowser(
  environment: NodeJS.ProcessEnv = process.env,
): string {
  const entries = pathEntries(environment);
  const suffixes = executableSuffixes(environment);
  for (const name of BROWSER_NAMES) {
    for (const entry of entries) {
      for (const suffix of suffixes) {
        const candidate = join(
          entry,
          extname(name) ? name : `${name}${suffix}`,
        );
        if (isExecutable(candidate)) return realpathSync(candidate);
      }
    }
  }
  throw new Error(
    `No supported system browser found in PATH. Expected, in order: ${BROWSER_NAMES.join(", ")}. Install one through the environment owner; this test does not download browsers.`,
  );
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

export function systemBrowserLaunchOptions(
  environment: NodeJS.ProcessEnv = process.env,
  policy: SystemBrowserLaunchPolicy = {},
): SystemBrowserLaunchOptions {
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
    executablePath: findSystemBrowser(environment),
    headless: !headful,
    args,
  };
}
