# PX-YY: Short title

## Control

- Status: Draft
- Intended implementation model: Luna, Terra, or Sol
- Required review: None, Terra, or Sol
- Depends on: packet IDs, commits, or `none`
- Blocks: packet IDs or `none`

## Goal

State the one observable outcome produced by this packet.

## Why this packet exists

Identify the relevant roadmap requirement and current implementation boundary.
Include links to the source plan and the small set of code files the task must
understand.

## Fixed decisions

List every design choice the implementation task must treat as settled. Give
exact type names, signatures, persistence semantics, ordering rules, and error
behavior when those details matter.

## Allowed changes

List the files or directories the task may edit. Prefer one package. State
whether new files may be added.

## Required behavior

Describe the implementation contract as independently testable statements.
Include compatibility behavior that must remain unchanged.

## Tests to add or update

List the required cases. Prefer table-driven tests, fake clocks, fake clients,
and small invented seasons over live requests.

## Verification

List every command the implementation task must run. Include a narrow command
and the full repository test command when practical.

## Non-goals

List nearby work that belongs to later packets. This prevents a bounded task
from turning into an architectural refactor.

## Stop conditions

Describe discoveries that require the task to stop and report instead of
guessing or expanding scope.

## Handoff

Require a concise report containing:

- files changed;
- behavior implemented;
- verification commands and outcomes;
- deviations from the packet;
- unresolved questions or recommended follow-up.

