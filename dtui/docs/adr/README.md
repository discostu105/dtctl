# Architecture Decision Records

Short records of the decisions that shaped dtui, in the spirit of
[Nygard ADRs](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions):
context, decision, consequences. The design docs under
[../design/](../design/) carry the detail; an ADR is the durable "why" you
can read in two minutes.

| # | Decision |
|---|---|
| [0001](0001-dql-is-the-substrate.md) | DQL is the substrate, not the interface |
| [0002](0002-bubbletea-stack.md) | bubbletea + lipgloss as the UI stack |
| [0003](0003-declarative-view-catalog.md) | Declarative view catalog, bespoke screens as the exception |
| [0004](0004-separate-module-and-binary.md) | dtui is a separate Go module and binary; `dtctl tui` forwards |
| [0005](0005-dtctl-owns-configuration.md) | dtctl owns configuration; dtui is a pure consumer |
| [0006](0006-read-first-safety-gated.md) | Read-first UI; mutations are safety-gated *(superseded by 0011)* |
| [0007](0007-scope-honesty.md) | Scope honesty: never claim a scope the query didn't apply |
| [0008](0008-one-owner-per-key.md) | One owner per key; digits follow the visible numbering |
| [0009](0009-navigate-dont-draw.md) | Navigate the topology, don't draw it |
| [0010](0010-curated-defaults-runtime-discovery.md) | Curated defaults, runtime discovery for the rest |
| [0011](0011-strictly-read-only.md) | dtui is strictly read-only; mutations are not planned |

New ADRs: next number, same three sections, `Accepted`/`Superseded` status.
Record a decision when reversing it would ripple through more than one view.
