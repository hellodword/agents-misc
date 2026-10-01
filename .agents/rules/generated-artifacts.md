# Generated Project Assets

- Decide ownership by role and consumer. Required generated bindings, imported clients, parser output, offline query metadata, synthetic database fixtures, and visual golden baselines can be tracked inputs. Inspect them for sensitive or machine-private content.
- A filename or extension alone does not determine whether an asset belongs in version control.
- For reproducible generated sources, retain authoritative inputs, the project-pinned generator, and a documented command. Inspect unexpected timestamps, absolute paths, identifiers, ordering, and size.
- Follow the established generation workflow. Add a script or shared command when it makes durable generation easier to repeat, without requiring a particular command name.
- Review the generated diff and validate the affected consumer. Repeat generation to check stability when the generator, pipeline, or reproducibility assumptions change, or when the output looks unstable; routine content updates do not always need a second run.
- Golden baseline updates require evidence that the new expected result is intended. Do not accept output merely to silence a failing test.
- Follow upstream conventions in patch projects. Apply compatibility requirements according to consumers, not merely because an interface is generated.
