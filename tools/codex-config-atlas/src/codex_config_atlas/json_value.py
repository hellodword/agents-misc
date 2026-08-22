from __future__ import annotations

import json
import math
from typing import Any


def canonical_json_key(value: Any) -> str:
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        if not math.isfinite(value):
            raise ValueError("JSON numbers must be finite")
        if value == 0:
            return "0"
        if value.is_integer():
            return str(int(value))
        return json.dumps(value, allow_nan=False, separators=(",", ":"))
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    if isinstance(value, list):
        return f"[{','.join(canonical_json_key(item) for item in value)}]"
    if isinstance(value, dict):
        if not all(isinstance(key, str) for key in value):
            raise ValueError("JSON object keys must be strings")
        items = (
            f"{json.dumps(key, ensure_ascii=False)}:{canonical_json_key(value[key])}"
            for key in sorted(value, key=lambda item: item.encode("utf-16-be"))
        )
        return f"{{{','.join(items)}}}"
    raise ValueError(f"value is not JSON-compatible: {value!r}")
