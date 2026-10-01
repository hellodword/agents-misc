# Nix-backed Playwright Browser

Use this reference when a project provisions its Playwright browser through Nix. Preserve an existing explicit project browser and development-shell policy unless the task authorizes changing it.

## Pin both sides of the compatibility boundary

- Let the project-owned `flake.lock` pin the nixpkgs revision and therefore the browser build.
- Let the established package-manager lock pin `@playwright/test` or `playwright`.
- Do not add a second handwritten browser version string. Update Nix inputs only through Nix and dependency locks only through their owning package manager.
- Playwright documents custom `executablePath` use as a compatibility risk compared with its bundled browsers. Run a focused real-browser smoke flow on initial setup or relevant browser, Playwright, or launch-configuration changes. Unrelated lockfile changes do not need that smoke test. Resolve incompatibility through supported input/dependency updates or configuration changes, without a silent browser fallback.

## Development shell

Add browser settings to the appropriate development shell. Start with `devShells.default` for a new setup, preserve established named shells, and isolate environments when there is a concrete need. This excerpt shows only the browser settings:

```nix
default = pkgs.mkShell {
  PLAYWRIGHT_NIX_BROWSER_PATH = pkgs.lib.getExe pkgs.chromium;
  PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
};
```

`pkgs.chromium` is the default free Chrome-family browser. If the product specifically requires branded Google Chrome, first verify the project's supported systems and obtain explicit authorization for the unfree dependency, then import nixpkgs with a narrow `allowUnfreePredicate` and use `pkgs.google-chrome`. Do not broaden unfree acceptance for unrelated packages.

Run dependency installation and E2E through the same selected shell so the download policy and executable path are present. For a default shell:

```sh
nix develop --command npm ci
nix develop --command npm run e2e
```

Use the repository's established package manager; the examples use npm. Keep the package script as the durable E2E interface; a thin Just recipe may invoke the Nix command.

## Playwright configuration

Copy the linked helper into project-owned test support and call it from `playwright.config.ts`:

```ts
import { defineConfig } from "@playwright/test";
import { nixBrowserLaunchOptions } from "./tests/support/playwright-nix-browser";

export default defineConfig({
  use: {
    launchOptions: nixBrowserLaunchOptions(process.env),
  },
});
```

The helper requires an absolute executable path from `PLAYWRIGHT_NIX_BROWSER_PATH`. It never searches `PATH`, selects an ambient browser, downloads a browser, or falls back. Keep any project-selected headful policy explicit when passing the helper options.

## Focused validation

For provisioning or compatibility work, verify the executable and smoke flow, adapting the shell and package command names to the project:

```sh
nix develop --command bash -c 'test -x "$PLAYWRIGHT_NIX_BROWSER_PATH"'
nix develop --command bash -c '"$PLAYWRIGHT_NIX_BROWSER_PATH" --version'
nix develop --command npm run e2e -- --grep @smoke
```

Broader flake checks are needed only for affected adopted output/check wiring or a project gate. For compatibility work, record the browser and Playwright versions, relevant shell/launch configuration, and smoke-flow result. A browser-version command alone does not validate Playwright compatibility.
