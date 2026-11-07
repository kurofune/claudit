# Memory

- claudit's `.djinn/seats/*` charters and duties are resynced from djinn's `internal/djinn/seat/templates/<seat>/` (`chore(djinn): resync` commits). /create-bead refuses a `touches` entry under `.djinn/seats/` and claudit has no templates dir, so a seat-rule bead files with no `touches` and names the djinn template as the lasting fix.
- claudit has no `docs/adr/`; DECISIONS.md `## Decided` rows carry their evidence inline. Seat prompts that cite docs/adr/ files point at nothing here.
