## Contributing

### Local setup

- Install hooks:

```bash
pre-commit install
pre-commit install --hook-type commit-msg
```

- Run checks:

```bash
pre-commit run --all-files
```

- Update hook versions:

```bash
pre-commit autoupdate
pre-commit run --all-files
```

> [!TIP]
> If you want a single command for updating + validating, run:
> `pre-commit run --hook-stage manual pre-commit-autoupdate`

### Secret scanning (ggshield)

`ggshield` runs as a **pre-push** hook by default. If you want it enabled, set
`GITGUARDIAN_API_KEY` in your environment.

### Commit messages

This repository follows **Conventional Commits** and enforces rules via
`.commitlintrc.yaml` (validated at commit time by a git hook).

Example:

```text
docs(scaffold): add baseline repo hygiene files
```

### Pull requests

- Keep changes focused (avoid bundling refactors with feature work)
- Do not commit secrets (keys, tokens, passwords, credentials)
