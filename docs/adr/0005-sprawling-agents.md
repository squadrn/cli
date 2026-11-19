# 0005 — Name: Sprawling Agents (`sprawl`, alias `sra`)

Status: accepted (v1.0.0); the name is superseded by 0010 (Squadrn) — the structure stays

## Context

"squad" collided with tools in the same category (`smtg-ai/claude-squad`, `github/squad`). The
owner wanted a name with a technical, artistic and underground character, provider-neutral for
Claude, Codex, Gemini and others.

## Decision

- Product name **Sprawling Agents** (after Gibson's Sprawl); repository `sprawling-agents`.
- Technical slug **`sprawl`** for every fixed identifier: `~/.config|.local/share|.local/state|.cache/sprawl`,
  `SPRAWL_*`, plugin `sprawl@sprawl`, git key `sprawl.protectedBranches`, tmux options `@sprawl_*`,
  release assets `sprawl_<ver>_<os>_<arch>.tar.gz`.
- Default command **`sprawl`**, default alias **`sra`** (SpRawling Agents). Rejected: `sa` shadows
  `/usr/sbin/sa` (process accounting, macOS and Linux `acct`); `spa` reads as "Single Page
  Application" to web developers.
- Versioning restarts at **1.0.0**, the first published release; ccx (2.x) and squad (3.0.0) were
  never published and are recorded as history.
- Previous names are listed once (`config.PreviousNames = ["squad", "ccx"]`); their config,
  data dirs, plugins, command links and git keys are migrated or honoured from that list.
- Structure from 0004 (fixed identity vs. user-chosen command vs. `provider/name` accounts) stays.

## Consequences

- Accepted risk: `2youg1/sprawling-agents` (MPL-2.0, active) uses the same name for "a city of AI
  agents on your machine". Searches may mix the two; revisit if it becomes a brand conflict.
- Installing over squad or ccx keeps settings but not their command names: users get `sprawl` +
  `sra` unless they choose otherwise.
