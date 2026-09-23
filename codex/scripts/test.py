from __future__ import annotations

import argparse

from common import (
    add_repo_root_argument,
    apply_patch_series,
    changed_tree_digest,
    clone_pinned_checkout,
    ensure_pinned_head,
    ensure_real_index_clean,
    json_stdout,
    load_manifest,
    maintenance_sandbox,
    main_wrapper,
    patch_paths,
    require_git_worktree,
    run_generation_sequence,
    run_manifest_validation,
    validate_patch_directory,
    validate_zstd_fixture_tree,
)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Validate application, generation, and targeted Codex behavior"
    )
    add_repo_root_argument(parser)
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root)
    src = manifest.worktree
    require_git_worktree(src)
    ensure_real_index_clean(src)
    ensure_pinned_head(src, manifest.upstream)
    with maintenance_sandbox(manifest, "test-workspace") as temporary:
        validation = temporary / "src"
        clone_pinned_checkout(manifest, validation)
        validate_patch_directory(manifest)
        patches = patch_paths(manifest)
        binary_patches = apply_patch_series(validation, patches)
        before_generate = changed_tree_digest(validation)
        first_generation_digest, second_generation_digest = run_generation_sequence(
            manifest, validation, twice=True
        )
        after_generate = changed_tree_digest(validation)
        if after_generate != before_generate:
            raise RuntimeError(
                "tracked generated artifacts drift after applying the patch series"
            )
        validate_zstd_fixture_tree(validation)
        seen = run_manifest_validation(manifest, validation)
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
