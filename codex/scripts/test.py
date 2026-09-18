from __future__ import annotations

import argparse
import tempfile
from pathlib import Path

from common import (
    add_repo_root_argument,
    apply_patches,
    changed_tree_digest,
    ensure_pinned_head,
    ensure_real_index_clean,
    json_stdout,
    load_manifest,
    main_wrapper,
    patch_paths,
    require_git_worktree,
    run,
    run_generation_sequence,
    run_upstream,
    validate_binary_patches,
    validate_patch_directory,
)


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate application, generation, and targeted Codex behavior")
    add_repo_root_argument(parser)
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root)
    src = manifest.worktree
    require_git_worktree(src)
    ensure_real_index_clean(src)
    ensure_pinned_head(src, manifest.upstream)
    with tempfile.TemporaryDirectory(prefix="codex-test-") as temporary:
        validation = Path(temporary) / "src"
        run(
            [
                "git",
                "-c",
                "protocol.file.allow=always",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(src),
                str(validation),
            ]
        )
        run(["git", "checkout", "--quiet", "--detach", manifest.upstream.revision], cwd=validation)
        validate_patch_directory(manifest)
        patches = patch_paths(manifest)
        binary_patches = validate_binary_patches(validation, patches)
        apply_patches(validation, patches, check_only=False)
        before_generate = changed_tree_digest(validation)
        first_generation_digest, second_generation_digest = run_generation_sequence(
            manifest, validation, twice=True
        )
        after_generate = changed_tree_digest(validation)
        if after_generate != before_generate:
            raise RuntimeError("tracked generated artifacts drift after applying the patch series")
        run_upstream(manifest, manifest.upstream.validation_command, cwd=validation)
        seen: set[tuple[str, ...]] = set()
        for patch in manifest.patches:
            for command in patch.tests:
                if command in seen:
                    continue
                seen.add(command)
                run_upstream(manifest, command, cwd=validation)
        for command in manifest.upstream.regression_commands:
            if command in seen:
                continue
            seen.add(command)
            run_upstream(manifest, command, cwd=validation)
    json_stdout(
        {
            "ref": manifest.upstream.ref,
            "revision": manifest.upstream.revision,
            "patches": [patch.file for patch in manifest.patches],
            "generationStable": True,
            "generationDigest": first_generation_digest,
            "secondGenerationDigest": second_generation_digest,
            "binaryPatches": list(binary_patches),
            "regressionCommands": len(manifest.upstream.regression_commands),
            "targetedCommands": len(seen),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
