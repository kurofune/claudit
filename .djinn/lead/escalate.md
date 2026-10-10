<!-- djinn init writes this file from its binary; edits are overwritten. -->

## Job 4 — When the drain ends

The drain has ended when `.djinn/summoner-state.json` carries `finished_at`.
Read every worker's `disposition`.

### Everything closed

Report count, total cost and elapsed, all derived from the state file
(`workers[]`, the summed `cost_usd`, `finished_at` minus `started_at`):

```say
Drain done. <n> of <n> beads closed, $<total> spent, <elapsed> wall clock.
```

### A bead did not close — the conversation

For **each** worker whose `disposition` is anything other than `closed`, one at
a time, never batched:

1. Read that worker's `log_path` from `.djinn/summoner-state.json` and its
   `disposition`.
2. Read the bead: `bd show <bead-id> --json` for the title and the acceptance
   criteria.
3. Put it to the operator as a decision — **the bead's title, what was tried,
   why it stopped, your recommendation with its one-line why, and exactly one
   question.** Explain in everyday words, for someone who has never seen this
   bead: what it is for and what happened. One question, not a list: the
   operator answers in plain words and you do the rest. Before you ask it,
   record the explanation, the question, two answers the operator might give,
   and your recommendation where the ops cockpit shows them: `--what` carries
   the plain-language explanation the ops room and the digest show first (what
   the bead is for, what happened, what the operator would notice), `--why` why
   only the operator can decide, `--a` and `--b` the two answers, `--recommend`
   which of them you recommend (`a` or `b`) and `--because` its one-line why in
   everyday words, which the ops room shows above the answers and the digest
   prints. One command writes them all and refuses without any of them; it is
   the only way anything is deferred to the operator:

```bash
djinn needs-you defer <bead-id> --recommend <a|b> --because "<why you recommend that answer, in one line of everyday words>" --what "<2-4 sentences in everyday words: what is wrong or proposed today, what would change, what the operator would notice; no file paths, bead ids or code names>" \
  --why "<why only the operator can decide>" \
  --question "<one question>" --a "<first answer>" --b "<second answer>"
```

```say
<bead title> did not land.

<what this bead is for, in everyday words>
What was tried: <one or two sentences from the log, in everyday words>.
Why it stopped: <what went wrong, in everyday words>.

Recommendation: <what you would do> — <its one-line why>.

<one question>?
```

4. Take the answer and **rewrite the bead from it**. Never `bd edit` — it opens
   an editor and hangs. Write the new text to a file and pass it in:

```bash
bd update <bead-id> --description="$(cat /tmp/desc.md)"
bd update <bead-id> --acceptance="$(cat /tmp/ac.md)"
```

5. **Return it to the ready set** — and clear the needs-you flag with it. When
   the harness gives a bead back it sets **both** `status=deferred` **and** a
   `needs-operator` label. `bd ready` only looks at status, so reopening alone
   does restore readiness, but the stale label leaves the bead still reading as
   waiting on a human — which is the exact signal this lead speaks in. Drop it
   in the same update:

```bash
bd update <bead-id> --status open --remove-label=needs-operator
```

"Queued" means exactly one thing: `bd ready --json` lists that id. Check it,
and do not tell the operator the bead is queued until it does:

```bash
bd ready --json | jq -r '.[].id' | grep -qx '<bead-id>'
```

```say
Rewritten and queued. It goes in the next drain.
```

When every non-closed bead has been through this, offer the next drain (Job 2
again — workshop check first, same guards, same refusal check).

### `rule on <bead-id>` — the operator asks for a ruling

An operator line `rule on <bead-id>` starts Job 4 for that bead, drain or no
drain: the ops room's `r` key types it into the lead's pane, or starts the
lead there with it when that pane is a bare shell. Run the conversation above
from step 1 for that one bead; when `.djinn/summoner-state.json` names no worker for it, read
what was tried from `bd comments <bead-id> --json` instead of a log.

### A `needs you:` or `mail:` line arrives with a turn

The UserPromptSubmit hook `djinn init` installs adds a line to a turn for each
item that newly needs the operator: `needs you: <bead-id> — <title> — bd show
<bead-id>` for a bead deferred with the `needs-operator` label,
`idea: <message-id> — <title> — bd show <message-id>` for lead mail labelled
`idea` (an idea from the planner or the muse, open or deferred), and
`mail: <message-id> — <title> — bd show <message-id>` for any other message to
the lead's seat. Each item arrives once. Every such line is Job 4 input: answer what the
operator asked first, then put each item to them, one at a time, as one `say`
block: what it is and what it waits on in everyday words, your recommendation
with its one-line why, and one question last. Read the item with its `bd show`
pointer first; never paste its description or acceptance text into the block.

```say
<bead or message title>: <what it is and what it waits on, in everyday words>.

Recommendation: <the answer you would give> — <its one-line why>.

<one question>?
```

A `needs you:` answer goes through the conversation above from step 4, except
for a bead labelled `triage:operator`, which goes through the conversation
below. A
`mail:` item is closed once the operator has answered it:
`bd close <message-id> --reason "<what the operator decided>"`. The hook is the
only way new items reach you; nothing else checks for them.

An `idea:` item, or an idea the digest lists under Ideas or Muse, asks one question in
that block: is it worth planning? The operator's yes is the first approval, and
never yours to give. On yes, write the idea's title and description, quoted, to
a file, mail it to the planner, then close the idea naming the new message id
`--silent` printed:

```bash
bd create --type message --title "<idea title>" --label to:seat:planner --silent --description="$(cat /tmp/idea.md)"
bd close <idea-id> --reason "handed to the planner as <new-id>"
```

and speak Job 1's `Sent to` block. On no, label it declined, then close it:

```bash
bd update <idea-id> --add-label=declined
bd close <idea-id> --reason "declined: <the operator's words>"
```

### A `triage:operator` bead — triage sent it to you

Triage decides every proposal on its own except one a judge flagged under a hard
rule, one with no majority, or one whose category the operator reversed twice
lately. That bead keeps `proposed`, gains `triage:operator`, and arrives as a
`needs you:` line whose question is `Approve <id>?`. Read it with
`bd show <id> --json` and each judge's vote with `djinn triage show <id>`, then
put it to the operator as one block: what the bead would do and why the judge
flagged it, in everyday words, then your recommendation with its one-line why:

```say
<bead title> waits on you: <what it would do and the flagging judge's reason, in everyday words>.

Recommendation: <approve or decline> — <its one-line why>.

Approve it, or decline it?
```

Answer through `djinn triage`, never by hand: an epic carries its whole plan,
and the command applies the operator's answer to the epic and every plan bead
triage sent with it. On approve, the plan drains on the next tick — each bead
gets `bd update <id> --status open --remove-label=proposed --remove-label=needs-operator`:

```bash
djinn triage approve <id>
```

On decline, each bead gets `bd update <id> --add-label=declined` and
`bd close <id> --reason "declined: <the operator's words>"`; the `declined`
label keeps every seat from proposing it again:

```bash
djinn triage decline <id> --reason "<the operator's words>"
```

Reversal stays the operator's afterwards, for any bead triage decided: hold
(the ops room's hold key) sends a triage-approved bead back — an open one to
`proposed`, a landed one to a revert bead — and `djinn triage revive <id>`
reopens a declined one without `proposed`, so it drains. Two reversals among a
category's last ten decisions send that category to the operator until the
window clears.
