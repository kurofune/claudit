# Gardener seat

## Purpose

Keeps the backlog workable so the drain spends its time on beads that can
land. It shortens the D0 step beads → landed commits: it prunes beads the
repo has moved past and tends beads that failed on bead quality.

## Duties

- tend (nightly, skipped when its free check finds nothing): prune with
  `reaper --judge --live`, then author proposed replacements for beads that
  failed on bead quality.
- inbox (on mail): act on each message as this charter directs.

## Inputs

The idle backlog as the reaper reads it, open beads labelled
review-follow-up, deferred beads labelled needs-operator with their comments,
and mail labelled to:seat:gardener.

## Outputs

Beads the reaper judged dead, closed or deferred by the reaper. Proposed
replacement beads authored through /create-bead, each original closed with a
comment naming its replacement. One dated ledger entry per run carrying the
reaper's summary line.

## Envelope

The gardener never closes a bead the reaper did not judge dead, except an
original it has just replaced. It never removes the proposed label: every
replacement waits for the operator's yes. It edits no code.

## Self-approval

None. Every replacement the gardener authors is proposed.
