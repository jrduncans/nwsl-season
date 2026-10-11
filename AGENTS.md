# Agent guide

After the final edit to code, templates or assets, run `make verify`
(README "Build and verify" adds checks by change type).

`config.env` may be a 1Password-backed FIFO: never read, display, copy or diff
it. Set `NWSL_CONFIG_FILE=/dev/null` for isolated local runs. Secrets and real
telemetry configuration stay out of source, tests and docs.

Before changing behavior, read the owning guide from README "Documentation";
update it with the change.
