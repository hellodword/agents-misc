# GitHub Actions

Add or change a GitHub Actions workflow only when the user explicitly requests that workflow change.

- For a new workflow, default to `push` on `master` plus `workflow_dispatch`; do not add a pull-request trigger without a requirement.
- Declare the minimum job/workflow permissions and raise them only for a documented step.
- First-party actions from `actions/*` and `github/codeql-action` may be selected after verifying the current stable major in official sources. Use its major tag and accept that the tag is mutable.
- Never use a branch tag such as `@main`.
- Use an action from any other owner only when the user explicitly names or authorizes that owner/action; verify source, version, permissions, and supply-chain behavior.
- Do not add caching, artifact publishing, deployment, secrets, cloud authentication, or release behavior by default.
- Keep shell input structured and quote expressions at trust boundaries. Avoid executing unreviewed content from pull requests or refs.
- Use Nix disk preparation, installation, container-storage workaround, or inherited-cache recipes only under the exact conditions in the Nix workflow reference.
- Never run the GitHub-hosted runner exception on local or self-hosted machines.
- Validate workflow syntax and the project commands invoked by changed steps.
