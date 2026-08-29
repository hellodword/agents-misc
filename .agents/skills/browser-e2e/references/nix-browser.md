# Nix-backed Playwright Browser

Use this reference when a project provisions its Playwright browser through Nix. Preserve an existing explicit project browser and development-shell policy unless the task authorizes changing it.

## Pin both sides of the compatibility boundary

- Let the project-owned `flake.lock` pin the nixpkgs revision and therefore the browser build.
- Let the established package-manager lock pin `@playwright/test` or `playwright`.
- Do not add a second handwritten browser version string. Update Nix inputs only through Nix and dependency locks only through their owning package manager.
- Playwright documents custom `executablePath` use as a compatibility risk compared with its bundled browsers. Run a focused real-browser smoke flow after either lock changes; report incompatibility instead of falling back to a host or downloaded browser.

## Development shell

When the project has no explicit shell-isolation contract, add the browser path to `devShells.default` with the other development tools. Preserve an established named shell when it already owns the E2E contract. This excerpt shows only the browser settings:

```nix
default = pkgs.mkShell {
  PLAYWRIGHT_NIX_BROWSER_PATH = pkgs.lib.getExe pkgs.chromium;
  PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
};
```

`pkgs.chromium` is the default free Chrome-family browser. If the product specifically requires branded Google Chrome, first verify the project's supported systems and obtain explicit authorization for the unfree dependency, then import nixpkgs with a narrow `allowUnfreePredicate` and use `pkgs.google-chrome`. Do not broaden unfree acceptance for unrelated packages.

Run dependency installation and E2E through the same default shell so the download policy and executable path are present:

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

Run these in order, adapting the shell and package command names to the project:

```sh
nix develop --command bash -c 'test -x "$PLAYWRIGHT_NIX_BROWSER_PATH"'
nix develop --command bash -c '"$PLAYWRIGHT_NIX_BROWSER_PATH" --version'
nix develop --command npm run e2e -- --grep @smoke
```

Run `nix flake check` only when checks or broad flake wiring are established project interfaces. Record the locked nixpkgs input, selected browser attribute and reported version, locked Playwright version, development shell, display mode, and smoke-flow result. A browser-version command alone does not validate Playwright compatibility.
