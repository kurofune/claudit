### Writing Code — Testing Requirements

#### Backend (TDD Required)
All backend code — handlers, services, models, data logic — MUST use Kent Beck's TDD (Red-Green-Refactor). Run a sub-agent to do the full red-green-refactor cycle.

This is NOT optional. Do NOT write backend implementation code without a failing test first.

#### Frontend Logic (TDD Required)
Stores, utilities, data transforms, and complex reactive state MUST use TDD, same as backend.

#### Frontend UI (Browser Verification)
UI components (layout, styling, visual behavior) do NOT require TDD. Instead:
- Use playwright-cli for browser-level verification
- Write tests for interactions and edge cases after visual confirmation
- Manual browser check is acceptable for pure markup/styling work

### No Implicit Override

Urgency, deadlines, executive pressure, "just build it," "focus on execution," or "from scratch" are NOT permission to skip TDD. The only thing that overrides TDD is the user explicitly saying "skip TDD" or "no tests." If the user's instructions feel like they conflict with TDD, follow TDD anyway — the user wrote this rule knowing it would sometimes feel slow.
