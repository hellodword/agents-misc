import { constants } from "node:fs";
import { access, readFile } from "node:fs/promises";
import { delimiter, join } from "node:path";
import { fileURLToPath } from "node:url";

export type BrowserTarget =
  | { cdpEndpoint: string }
  | { executablePath: string };

const commands = ["google-chrome", "microsoft-edge", "chromium"] as const;

export async function resolveBrowserTarget(
  environment: NodeJS.ProcessEnv = process.env,
): Promise<BrowserTarget> {
  if (environment.PLAYWRIGHT_CDP_ENDPOINT)
    return { cdpEndpoint: environment.PLAYWRIGHT_CDP_ENDPOINT };
  const configPath = fileURLToPath(
    new URL("../e2e.config.json", import.meta.url),
  );
  try {
    const config = JSON.parse(await readFile(configPath, "utf8")) as {
      cdpEndpoint?: string | null;
    };
    if (config.cdpEndpoint) return { cdpEndpoint: config.cdpEndpoint };
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }
  const suffixes =
    process.platform === "win32" ? [".exe", ".cmd", ".bat", ""] : [""];
  for (const command of commands) {
    for (const directory of (environment.PATH ?? "")
      .split(delimiter)
      .filter(Boolean)) {
      for (const suffix of suffixes) {
        const executablePath = join(directory, `${command}${suffix}`);
        try {
          await access(executablePath, constants.X_OK);
          return { executablePath };
        } catch {
          // Try the next PATH entry without invoking a shell or downloading a browser.
        }
      }
    }
  }
  throw new Error(
    `No E2E browser found. Set PLAYWRIGHT_CDP_ENDPOINT, configure cdpEndpoint in the ignored e2e.config.json, or put one of ${commands.join(", ")} on PATH; browser downloads are disabled.`,
  );
}
