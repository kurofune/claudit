# Memory

- The charter's ## Self-approval is [TODO], so it names nothing. A follow-up re-authored as a real-defect bug is filed `proposed` (goes to triage), not `seat-approved`, until the operator fills that section in.
- `bd create --graph` accepts a `parent_id` that names a closed epic. Follow-ups re-authored under an already-closed epic file without error.
- The lead's display name (`djinn theme show`, seats.lead) is the default "lead".
- JS tests: `npm test` (node --test jstest/**/*.test.js). A web fix's touches list its jstest/ file.
- Triage: a gardener `garden:seat-contradiction` bead that rewrites a seat's approval or closing rule gets the lead's hard-rule flag changes-how-the-workshop-decides, so it goes to the operator (claudit-cko, claudit-df0 on 2026-11-07). Its edit lands in .djinn/seats/, which is a lead attended edit plus the matching djinn template change, never drain work.
- Review follow-up Location lines drift: line numbers name the code at review time, not HEAD (claudit-ovu named :259, the code was at :382). Find the code by symbol before judging it.
- `djinn follow-ups --pending` exits 1 with no output when nothing is pending.
