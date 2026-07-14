from __future__ import annotations

import json
from typing import Any


def _index_defaults(defaults: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    return {item["path"]: item for item in defaults}


def build_defaults_diff(
    from_version: str,
    to_version: str,
    from_defaults: list[dict[str, Any]],
    to_defaults: list[dict[str, Any]],
) -> dict[str, Any]:
    from_index = _index_defaults(from_defaults)
    to_index = _index_defaults(to_defaults)
    paths = sorted(set(from_index) | set(to_index))
    changes: list[dict[str, Any]] = []
    unchanged = 0

    for path in paths:
        before = from_index.get(path)
        after = to_index.get(path)
        if before is None:
            changes.append(
                {
                    "kind": "default_added",
                    "path": path,
                    "to": after["value"],
                }
            )
            continue
        if after is None:
            changes.append(
                {
                    "kind": "default_removed",
                    "path": path,
                    "from": before["value"],
                }
            )
            continue
        if before["value"] != after["value"]:
            changes.append(
                {
                    "kind": "default_changed",
                    "path": path,
                    "from": before["value"],
                    "to": after["value"],
                }
            )
        else:
            unchanged += 1

    summary = {
        "added": sum(change["kind"] == "default_added" for change in changes),
        "removed": sum(change["kind"] == "default_removed" for change in changes),
        "changed": sum(change["kind"] == "default_changed" for change in changes),
        "unchanged": unchanged,
    }

    return {
        "from": from_version,
        "to": to_version,
        "summary": summary,
        "changes": changes,
    }


def render_defaults_diff_markdown(payload: dict[str, Any]) -> str:
    sections = {
        "default_added": "Added defaults",
        "default_changed": "Changed defaults",
        "default_removed": "Removed defaults",
    }
    by_kind: dict[str, list[dict[str, Any]]] = {key: [] for key in sections}
    for change in payload["changes"]:
        by_kind[change["kind"]].append(change)

    lines = [f"# Codex config default diff: {payload['from']} -> {payload['to']}", ""]
    for kind, title in sections.items():
        items = by_kind[kind]
        if not items:
            continue
        lines.append(f"## {title}")
        lines.append("")
        for item in items:
            lines.append(f"- `{item['path']}`")
            if "from" in item:
                lines.append(f"  - From: `{json.dumps(item['from'])}`")
            if "to" in item:
                lines.append(f"  - To: `{json.dumps(item['to'])}`")
        lines.append("")

    return "\n".join(lines).rstrip() + "\n"
