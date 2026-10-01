import { accessSync, constants, realpathSync, statSync } from "node:fs";
import { isAbsolute } from "node:path";
import type { LaunchOptions } from "@playwright/test";

export const NIX_BROWSER_PATH_VARIABLE = "PLAYWRIGHT_NIX_BROWSER_PATH";

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

export function nixBrowserLaunchOptions(
  environment: NodeJS.ProcessEnv = process.env,
  options: LaunchOptions = {},
): LaunchOptions {
  return {
    ...options,
    executablePath: findNixBrowser(environment),
  };
}
