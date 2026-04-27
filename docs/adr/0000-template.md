# ADR-NNNN: <decision title>

- **Status**: Proposed | Accepted | Superseded by ADR-XXXX | Deprecated
- **Date**: YYYY-MM-DD
- **Authors**: @handle, @handle
- **Deciders**: @handle (tech lead), @handle (product)

## Context

What's the situation today? What constraint or pressure forced this decision?
Be concrete — link to incidents, performance numbers, regulatory requirements.

## Decision

What did we decide? One sentence summary, then the specifics. Avoid wishy-washy
language ("we should consider…") — ADRs are accepted decisions, not proposals.

## Alternatives considered

What else did we look at, and why did we reject each? At least two real
alternatives. "Do nothing" is always a valid alternative to surface.

| Option | Pros | Cons | Why rejected |
|--------|------|------|--------------|
| Option A (chosen) | … | … | — |
| Option B | … | … | … |
| Option C | … | … | … |

## Consequences

What changes because of this? Both positive ("now we can scale to N tenants")
and negative ("two ways to do X exist for the next 6 months during migration").

## How we'll know it worked

A success criterion. Could be a metric ("p95 latency stays < 500ms after rollout"),
a process change ("on-call no longer paged for X"), or a milestone
("all clients migrated by Q3").

## Links

- Related PR / commit
- Background docs / RFCs
- Dependent ADRs
