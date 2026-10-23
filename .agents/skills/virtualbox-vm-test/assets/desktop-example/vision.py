"""One JSON request on stdin, one JSON response on stdout; no desktop input."""

import contextlib
import json
import sys


def main():
    try:
        request = json.load(sys.stdin)
        # Third-party diagnostics must not contaminate the protocol stream.
        with contextlib.redirect_stdout(sys.stderr):
            import cv2
            import numpy as np
            from airtest.core.cv import Template

            screen = cv2.imdecode(
                np.fromfile(request["screenshot"], dtype=np.uint8), cv2.IMREAD_COLOR
            )
            if screen is None:
                raise ValueError("Cannot decode screenshot")
            point = Template(request["template"], threshold=0.9, rgb=True).match_in(
                screen
            )
        print(
            json.dumps(
                {
                    "found": point is not None,
                    "point": [int(v) for v in point] if point is not None else None,
                }
            )
        )
        return 0
    except Exception as error:
        print(json.dumps({"error": str(error)}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
