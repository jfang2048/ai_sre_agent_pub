# Scripts Layout

The repository keeps operational scripts grouped by responsibility instead of mixing bootstrap, publish,
and test helpers in one flat directory.

Current groups:

- `scripts/bootstrap/`: local environment setup and optional dataset import helpers
- `scripts/publish/`: public-tree preparation and mirror publishing helpers
- `scripts/test/`: smoke and browser test entrypoints
- repository-root `scripts/*.sh`: stable day-to-day dev/build entrypoints, including role-focused controller/collector runtime and Docker helpers

Why this split exists:

- bootstrap concerns have different failure modes than runtime/dev scripts
- publish helpers must enforce stricter filtering and file-size audits than normal development flows
- test wrappers need isolated bootstrap behavior and should stay discoverable near CI commands
