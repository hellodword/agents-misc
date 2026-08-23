import { mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, test } from "vitest";
import { findNixBrowser } from "./browser";

const temporaryDirectories: string[] = [];

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { recursive: true, force: true });
  }
});

function temporaryBrowser(mode: number): string {
  const directory = mkdtempSync(join(tmpdir(), "agents-viewer-browser-test-"));
  temporaryDirectories.push(directory);
  const browser = join(directory, "chromium");
  writeFileSync(browser, "#!/bin/sh\nexit 0\n", { mode });
  return browser;
}

describe("the Nix browser contract", () => {
  test("rejects a missing browser path without searching PATH", () => {
    expect(() => findNixBrowser({ PATH: process.env.PATH })).toThrow(
      /PLAYWRIGHT_NIX_BROWSER_PATH is unset/,
    );
  });

  test("rejects a relative browser path", () => {
    expect(() =>
      findNixBrowser({ PLAYWRIGHT_NIX_BROWSER_PATH: "bin/chromium" }),
    ).toThrow(/must be an absolute executable path/);
  });

  test("rejects a non-executable browser path", () => {
    const browser = temporaryBrowser(0o600);
    expect(() =>
      findNixBrowser({ PLAYWRIGHT_NIX_BROWSER_PATH: browser }),
    ).toThrow(/is not an executable file/);
  });

  test("accepts and resolves an executable Nix browser path", () => {
    const browser = temporaryBrowser(0o700);
    expect(findNixBrowser({ PLAYWRIGHT_NIX_BROWSER_PATH: browser })).toBe(
      realpathSync(browser),
    );
  });
});
