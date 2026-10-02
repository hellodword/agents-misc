"""Real-browser flows and optional visual-review captures of the built static site."""

from __future__ import annotations

import argparse
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
from threading import Thread
import tomllib
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def smoke(site: Path, screenshots: Path | None) -> None:
    browser_path = os.environ.get("PLAYWRIGHT_NIX_BROWSER_PATH")
    if not browser_path or not Path(browser_path).is_file():
        raise RuntimeError(
            "Enter the codex-config-atlas Nix shell with its declared Chromium browser."
        )
    meta = json.loads((site / "data/versions.json").read_text())
    oldest, latest = meta["versions"][0]["version"], meta["versions"][-1]["version"]
    previous = meta["versions"][-2]["version"]
    if screenshots:
        screenshots.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(
        ("127.0.0.1", 0), partial(QuietHandler, directory=str(site))
    )
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{server.server_port}/"
    captures = []
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=browser_path)
            context = browser.new_context(
                viewport={"width": 1440, "height": 1000}, device_scale_factor=1
            )
            context.grant_permissions(["clipboard-read", "clipboard-write"])
            page = context.new_page()
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))

            def capture(name):
                if screenshots:
                    page.screenshot(path=str(screenshots / f"{name}.png"))
                    captures.append(
                        {
                            "id": name,
                            "route": page.url.removeprefix(url),
                            "viewport": page.viewport_size,
                        }
                    )

            def ready():
                expect(page.locator("#content")).to_have_attribute("aria-busy", "false")

            def no_overflow():
                assert page.evaluate(
                    "document.documentElement.scrollWidth <= innerWidth"
                ), page.viewport_size

            def url_parameters():
                return {
                    key: values[0]
                    for key, values in parse_qs(urlsplit(page.url).query).items()
                }

            page.goto(url)
            ready()
            expect(page.locator("#from-select")).to_have_value(previous)
            expect(page.locator("#to-select")).to_have_value(latest)
            expect(page.locator("#to-select option")).to_have_count(
                len(meta["versions"])
            )
            assert url_parameters() == {"view": "diff", "from": previous, "to": latest}
            capture("after-desktop-diff")
            # A wide historical comparison exercises long field names and descriptions.
            page.locator("#from-select").select_option(oldest)
            ready()
            assert page.locator(".field-card").count() > 0
            page.locator("#field-search").fill("nonexistent-atlas-test-field")
            expect(page.locator("#clear-filters")).to_be_visible()
            expect(page.locator("#field-search")).to_be_focused()
            capture("after-empty-search")
            page.locator("#clear-filters").click()
            expect(page.locator("#field-search")).to_be_focused()
            path = page.locator(".field-card summary code").first.inner_text()
            page.locator("#field-search").fill(path)
            assert all(
                path in text
                for text in page.locator(".field-card summary code").all_text_contents()
            )
            page.locator("#field-search").fill("")
            page.locator("[data-category='compatible']").click()
            assert page.locator(".diff-section:not(.category-compatible)").count() == 0
            page.locator("[data-category='all']").click()
            page.locator("#expand-all").click()
            assert page.locator(".field-card:not([open])").count() == 0
            page.locator("#collapse-all").click()
            assert page.locator(".field-card[open]").count() == 0
            first = page.locator(".field-card summary").first
            first.focus()
            first.press("Enter")
            expect(page.locator(".field-card").first).to_have_attribute("open", "")
            for width in (320, 390, 768, 1440):
                page.set_viewport_size(
                    {"width": width, "height": 844 if width < 700 else 1000}
                )
                no_overflow()
            page.set_viewport_size({"width": 390, "height": 844})
            page.evaluate("scrollTo(0, 0)")
            capture("after-mobile-diff")
            first.evaluate("element => element.scrollIntoView({block: 'start'})")
            capture("after-mobile-field")
            # Same-version and reverse comparisons preserve the selected direction.
            page.locator("#from-select").select_option(latest)
            ready()
            expect(page.locator(".empty-state")).to_contain_text("无配置差异")
            expect(page.locator("#copy-diff")).to_be_disabled()
            capture("after-no-difference")
            page.locator("#from-select").select_option(oldest)
            ready()
            page.locator("#swap-versions").click()
            ready()
            expect(page.locator("#from-select")).to_have_value(latest)
            expect(page.locator("#to-select")).to_have_value(oldest)
            page.locator("#copy-diff").click()
            assert "新增" in page.evaluate("navigator.clipboard.readText()")
            page.locator("#field-search").fill("model")
            page.locator("[data-category='compatible']").click()
            diff_parameters = {
                "view": "diff",
                "from": latest,
                "to": oldest,
                "q": "model",
                "category": "compatible",
            }
            assert url_parameters() == diff_parameters
            # Example preview, keyboard focus, copy and download all use the same artifact.
            page.locator("[data-view='example']").click()
            ready()
            expect(page.locator("#example-version")).to_have_value(oldest)
            expect(page.locator("#example-section")).to_have_value("all")
            expect(page.locator("#example-preview")).to_contain_text(
                f"# Version: {oldest}"
            )
            assert url_parameters() == {"view": "example", "version": oldest}
            page.locator("#example-version").select_option(latest)
            ready()
            page.locator("#example-section").select_option("approval_policy")
            expect(page.locator("#example-preview")).to_contain_text("approval_policy")
            assert url_parameters() == {
                "view": "example",
                "version": latest,
                "section": "approval_policy",
            }
            page.reload()
            ready()
            expect(page.locator("#example-section")).to_have_value("approval_policy")
            page.evaluate("scrollTo(0, 0)")
            capture("after-mobile-example")
            for width in (320, 390, 768, 1440):
                page.set_viewport_size(
                    {"width": width, "height": 844 if width < 700 else 1000}
                )
                no_overflow()
            capture("after-desktop-example")
            artifact = (site / f"data/versions/{latest}/example.toml").read_text()
            page.locator("#copy-example").click()
            assert page.evaluate("navigator.clipboard.readText()") == artifact
            with page.expect_download() as pending:
                page.locator("#download-example").click()
            download = pending.value
            assert download.suggested_filename == f"codex-{latest}.example.toml"
            assert Path(download.path()).read_text() == artifact
            assert tomllib.loads(artifact) == {}
            # Each view owns its URL; back/forward and tab switches retain selections.
            example_parameters = {
                "view": "example",
                "version": latest,
                "section": "approval_policy",
            }
            assert url_parameters() == example_parameters
            page.go_back()
            ready()
            assert url_parameters() == diff_parameters
            expect(page.locator("#from-select")).to_have_value(latest)
            expect(page.locator("#to-select")).to_have_value(oldest)
            expect(page.locator("#field-search")).to_have_value("model")
            expect(page.locator("[data-category='compatible']")).to_have_attribute(
                "aria-pressed", "true"
            )
            page.go_forward()
            ready()
            assert url_parameters() == example_parameters
            expect(page.locator("#example-version")).to_have_value(latest)
            expect(page.locator("#example-section")).to_have_value("approval_policy")
            page.locator("[data-view='diff']").click()
            ready()
            assert url_parameters() == diff_parameters
            page.locator("[data-view='example']").click()
            ready()
            assert url_parameters() == example_parameters
            page.locator("#example-version").select_option(oldest)
            ready()
            page.locator("#example-section").select_option("all")
            expect(page.locator("#example-preview")).to_contain_text(
                f"# Version: {oldest}"
            )
            page.reload()
            ready()
            expect(page.locator("#example-version")).to_have_value(oldest)
            expect(page.locator("#example-section")).to_have_value("all")
            assert url_parameters() == {
                "view": "example",
                "version": oldest,
            }
            # A held older request must not overwrite a newer selection.
            held = []
            page.route(
                f"**/versions/{oldest}/example.toml", lambda route: held.append(route)
            )
            page.reload(wait_until="domcontentloaded")
            expect(page.locator("#example-version")).to_be_visible()
            page.wait_for_function(
                "document.querySelector('#content').getAttribute('aria-busy') === 'true'"
            )
            capture("after-loading")
            page.locator("#example-version").select_option(latest)
            ready()
            assert held, "older request was not intercepted"
            held[0].fulfill(
                body=(site / f"data/versions/{oldest}/example.toml").read_text()
            )
            page.wait_for_load_state("networkidle")
            expect(page.locator("#example-preview")).to_contain_text(
                f"# Version: {latest}"
            )
            page.unroute(f"**/versions/{oldest}/example.toml")
            # Recoverable fetch errors leave the controls available.
            page.route(
                "**/example.toml",
                lambda route: route.fulfill(status=503, body="unavailable"),
            )
            page.reload()
            expect(page.locator("#retry")).to_be_visible()
            capture("after-fetch-error")
            page.unroute("**/example.toml")
            page.locator("#retry").click()
            ready()
            expect(page.locator("#example-preview")).to_be_visible()
            page.route(
                "**/versions.json",
                lambda route: route.fulfill(status=503, body="unavailable"),
            )
            page.reload()
            expect(page.locator("#retry-start")).to_be_visible()
            page.unroute("**/versions.json")
            page.locator("#retry-start").click()
            ready()
            # Existing mixed-parameter links remain readable and are normalized on load.
            page.goto(
                f"{url}?view=example&version={oldest}&section=all"
                f"&from={latest}&to={oldest}&q=model&category=compatible"
            )
            ready()
            assert url_parameters() == {
                "view": "example",
                "version": oldest,
            }
            expect(page.locator("#example-version")).to_have_value(oldest)
            expect(page.locator("#example-section")).to_have_value("all")
            page.goto(
                f"{url}?view=diff&from={oldest}&to={latest}"
                f"&version={oldest}&section=all&q=model&category=compatible"
            )
            ready()
            assert url_parameters() == {
                "view": "diff",
                "from": oldest,
                "to": latest,
                "q": "model",
                "category": "compatible",
            }
            expect(page.locator("#from-select")).to_have_value(oldest)
            expect(page.locator("#to-select")).to_have_value(latest)
            expect(page.locator("#field-search")).to_have_value("model")
            assert not errors, errors
            if screenshots:
                (screenshots / "captures.json").write_text(
                    json.dumps(
                        {
                            "browser": browser.version,
                            "versions": len(meta["versions"]),
                            "captures": captures,
                        },
                        ensure_ascii=False,
                        indent=2,
                    )
                    + "\n"
                )
            browser.close()
        print(
            f"Browser smoke passed: {len(meta['versions'])} versions; comparison, filters, keyboard, responsive layouts, examples, copy/download, URL state, stale requests and error recovery."
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", type=Path, required=True)
    parser.add_argument("--screenshots", type=Path)
    args = parser.parse_args()
    smoke(args.site.resolve(), args.screenshots.resolve() if args.screenshots else None)
