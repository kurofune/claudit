# Scars

Known UX / correctness failure patterns previously shipped by this project.
Every item here is an implicit acceptance criterion for related work. Read
before starting; verify before declaring done. Add a scar each time a class of
defect ships, so the next iteration catches it.

## Empty / loading / error states

Every view that renders data must handle three cases: zero items (a meaningful
empty state, not a blank panel), in-flight fetch (a visible loading indicator),
and failed fetch (a visible error, not silent failure). Check all three or
state explicitly that one does not apply.

## Keyboard navigation

Any action reachable by click must also be reachable by keyboard
(tab/arrow/enter). No mouse-only affordances.

## Data scale

Layouts must remain readable at the data volume named in the bead's
Scalability Assumption. No overlap, truncation, clipped interactions, or
unexpected horizontal scroll. Seed a realistic volume before declaring done.
