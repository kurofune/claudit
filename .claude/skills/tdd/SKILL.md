---
name: tdd
description: >
  Practice Kent Beck's Canon Test-Driven Development with strict Red-Green-Refactor
  discipline. Use this skill whenever the user mentions TDD, test-driven development,
  "write tests first", "red green refactor", "test list", "failing test first",
  "make the test pass", or asks to build any feature using a test-first approach.
  Also activate when the user says "let's TDD this", "drive this with tests",
  or references Canon TDD. This skill MUST be used for any test-first workflow —
  do not attempt TDD without it.
---

# Canon TDD — Kent Beck's Discipline

You are practicing Kent Beck's Canon TDD. This is not "write some tests then code."
This is a strict, phased discipline where each step has clear boundaries that must
never bleed into the next.

Your mantra: **Red. Green. Refactor. Nothing else.**

## Core Principles

These are not suggestions. They are the load-bearing walls of the process. When you
feel the urge to cut a corner, re-read the principle you're about to violate and
understand why it exists.

1. **Interface before implementation.** Tests drive the API surface (logical design).
   Implementation decisions (physical design) come later, during Green and Refactor.
   This separation exists because mixing them creates coupled, brittle tests that
   break when internals change — which is the opposite of what tests are for.

2. **One test at a time.** Never write a second test before the first one passes.
   Never write multiple tests speculatively "to save time." Each test shapes the
   implementation, and the second test is influenced by the code that emerged from
   the first. Writing tests in batches robs you of this feedback loop.

3. **Phases never overlap.** Writing a test (Red) is a different mental act than
   making it pass (Green), which is a different act than improving design (Refactor).
   Mixing them is the most common way to corrupt the process — it creates ambiguity
   about whether a failure is from new test logic or a refactoring mistake.

4. **The test list is living.** New scenarios discovered during any phase get added
   to the list — they do not get implemented immediately. This keeps your focus on
   the current cycle and prevents scope creep within a single Red-Green-Refactor.

5. **Behavioral focus.** Think in concrete scenarios (given X, when Y, then Z),
   not in implementation blueprints. Good test list items describe what the system
   does, not how it does it.

## Phase 0: Detect Test Framework

Before starting, determine the project's test framework and runner:

1. Look for config files: `vitest.config.*`, `jest.config.*`, `pytest.ini`,
   `pyproject.toml` (pytest section), `Cargo.toml` (Rust), `go.test`, `.mocharc.*`,
   `phpunit.xml`, `CMakeLists.txt` (Google Test/Catch2), or similar.
2. Look for existing test files to understand naming conventions (`.test.ts`,
   `.spec.ts`, `test_*.py`, `*_test.go`, `*_test.cpp`, etc.).
3. Determine the test run command (`npx vitest run`, `npm test`, `pytest`, `go test`,
   `cargo test`, `ctest`, etc.).
4. If no framework exists, ask the user which framework to set up. Do not assume.

Store this context mentally. You will need the run command throughout.

## Phase 1: Test List

When the user describes a feature, behavior change, or bug fix, your FIRST action is
to produce a Test List. Do NOT write any code yet.

### How to build the Test List

Analyze the desired behavioral change and list concrete scenarios:

- **Happy path cases** — the basic expected behaviors
- **Edge cases** — boundaries, empty inputs, minimums, maximums
- **Failure modes** — invalid inputs, missing data, permission errors
- **Existing behavior that must not break** — regression scenarios

### Test List format

Present the list to the user as a numbered markdown checklist:

```
### Test List

- [ ] 1. [Scenario description in plain English]
- [ ] 2. [Scenario description in plain English]
- [ ] 3. ...
```

### Test List rules

- Each item describes a BEHAVIOR, not an implementation detail.
  Good: "returns zero when the cart is empty"
  Bad: "test the calculateTotal method"
- Do NOT sneak implementation decisions into the list. No mention of specific data
  structures, algorithms, or internal methods unless they ARE the interface.
- Keep items small enough that each becomes exactly one test.
- Order matters. Sequence from simplest/most fundamental to most complex. The first
  test should be the one that forces you to create the minimum skeleton. Beck: "Test
  ordering affects the final code structure."

After presenting the test list, proceed immediately to Phase 2 with the first item.

## Phase 2: Red — Write Exactly One Failing Test

Pick the FIRST unchecked item from the Test List.

### Write the test

1. Create (or add to) the appropriate test file following project conventions.
2. The test MUST have:
   - **Arrange**: Concrete setup with real values (not vague placeholders)
   - **Act**: A single invocation of the interface being designed
   - **Assert**: Meaningful assertions about expected behavior
3. The test SHOULD:
   - Use the simplest possible assertion that verifies the behavior
   - Name itself after the scenario, not the implementation
   - Import from the module path where the production code WILL live

### Prohibitions during Red

- Do NOT write implementation code. Not even stubs. Not even empty files.
  The test must fail because the code does not exist yet (or does not handle
  this case). The compile/import error IS the first Red.
- Do NOT write more than one test.
- Do NOT write a test without meaningful assertions. `expect(true).toBe(true)`
  is not TDD, it is theater.
- Do NOT design the implementation. You are designing the INTERFACE — what the
  caller sees. Beck: "Chill. There will be plenty of time to decide how the
  internals will look later."

### Verify Red

Run the test suite. Confirm the new test FAILS and all previous tests still PASS.

Report:
```
RED: [test name] fails as expected.
Reason: [why it fails — import error, function not found, assertion mismatch, etc.]
Previous tests: all passing.
```

If the test passes without implementation changes, something is wrong. Either the
test is not testing new behavior, or the behavior already exists. Investigate.
Do not proceed to Green with a test that did not go Red.

## Phase 3: Green — Make It Pass (Minimally)

Write the MINIMUM code to make the failing test pass.

### Rules for Green

1. **Minimum means minimum.** If the test expects `return 0` for an empty case,
   literally return 0. Do not build the general solution yet. Beck calls this
   "obvious implementation" for trivial cases and "fake it" for non-trivial ones.
   Both are valid. Over-engineering is not.
2. **All tests must pass.** The new test AND every previous test. If a previous
   test breaks, fix it without deleting its assertions. If you cannot fix it
   without a significant design change, add that scenario to the Test List and
   back out.
3. **Create production files as needed.** Put them where the test's import
   statements expect them.

### Prohibitions during Green

- **Do NOT cheat to make tests pass.** This is the single most important rule. The
  temptation to take shortcuts is strong — resist it completely. Specifically:
  - Do NOT delete, comment out, or skip (`xit`, `test.skip`, `@pytest.mark.skip`)
    any test or assertion. Every assertion that existed before Green must still exist
    and still assert the same thing.
  - Do NOT weaken assertions (e.g., changing `toBe(42)` to `toBeTruthy()`, or
    changing `assertEqual` to `assertIsNotNone`). The assertion was written in Red
    for a reason — it encodes the expected behavior.
  - Do NOT modify a previously passing test to make it accommodate new code. If new
    code breaks an old test, the new code is wrong — fix the implementation, not the
    test.
  - Do NOT return hardcoded `true`/`false` to bypass validation logic that a test
    is checking. "Fake it" means returning a simple constant for the FIRST test to
    get the skeleton in place — it does NOT mean returning `true` to skip implementing
    real logic when the test expects real behavior.
  - Do NOT copy computed output into expected values. Run the code, see what it
    produces, then paste that into the assertion — this defeats the purpose of TDD.
    Expected values come from understanding the behavior, not from running the code.
- Do NOT refactor during Green. If you see duplication, ugly code, or a better
  structure — note it, but do not act on it. That is Refactor's job.
- Do NOT implement scenarios from the Test List that have no test yet. If you
  think "while I'm here I might as well handle the null case" — stop. Add it to
  the list if it is not there. Move on.

### Verify Green

Run the test suite. Confirm ALL tests pass. **Verify the test count has not decreased.**
If there were N tests before Green, there must be at least N tests after. If the count
dropped, you deleted or skipped a test — undo and try again.

Report:
```
GREEN: All tests passing ([N] total, was [M] before).
Implementation: [1-2 sentence summary of what you wrote]
```

## Phase 4: Refactor — Improve Design (Optional)

ONLY after Green, with all tests passing, you MAY refactor.

### What refactoring means here

- Remove duplication (but "duplication is a hint, not a command")
- Improve naming
- Extract functions or methods for clarity
- Simplify logic
- Improve performance IF the test list includes performance scenarios

### What refactoring does NOT mean

- Adding new behavior (that requires a new test first)
- Changing the public interface (that could break existing tests)
- Prematurely abstracting for hypothetical future requirements
- Rewriting working code because you "don't like" the fake-it approach
  (the next test will force generalization naturally)

### Rules for Refactor

1. Run tests AFTER every refactoring step. If tests break, undo immediately.
2. Keep refactoring steps small. One rename. One extraction. One simplification.
   Run tests between each.
3. If refactoring reveals a new scenario, add it to the Test List. Do not
   implement it now.

### Verify Refactor

Run the test suite. Confirm ALL tests still pass.

Report:
```
REFACTOR: All tests still passing.
Changes: [what you improved, or "No refactoring needed — code is clean."]
```

## Phase 5: Repeat

1. Check off the completed item in the Test List.
2. If the Test List still has unchecked items, return to Phase 2 with the next one.
3. If new items were added during the cycle, present the updated list.
4. Continue until the Test List is empty.

When the list is empty, announce:
```
TDD COMPLETE: All [N] scenarios tested and passing.
Test list is empty. All behavioral requirements are covered.
```

## State Tracking

Throughout the TDD session, display a status block when transitioning between phases.
This makes the current state visible and phase-skipping obvious:

```
--- TDD Status ---
Phase: [Red | Green | Refactor | Complete]
Test List: [X/Y completed]
Current test: [name or "none"]
All tests passing: [yes/no]
------------------
```

## Autonomous Operation

This workflow runs end-to-end without pausing for human confirmation between phases.
Build the test list and immediately begin the first Red phase. Run through each
Red-Green-Refactor cycle continuously. Continue through the entire test list until
complete. The status block at each phase transition lets the user follow along.

## Handling Requests That Break Discipline

If the user asks you to:

- **"Just write the implementation"** — Remind them that in Canon TDD, the
  implementation emerges from the tests. This matters because writing implementation
  first means the tests end up confirming what you wrote rather than specifying what
  you need. Offer to work through the test list quickly with tight cycles.
- **"Write all the tests first"** — Explain that batch-testing loses the feedback
  loop that makes TDD valuable. Each test shapes the implementation, and the next
  test is informed by that shape. Offer to keep cycles fast so it doesn't feel slow.
- **"Skip the test list"** — The test list is how you avoid wandering and missing
  scenarios. Without it, you'll either over-build or under-build. Offer to make it
  brief — even 3-4 items is better than none.
- **"This is taking too long"** — Acknowledge the concern. Offer shorter test lists
  and tighter cycles. But do not collapse Red-Green-Refactor into a single step —
  that eliminates the phase separation that catches mistakes.

If the user INSISTS after your explanation, comply — they are the user. But note
that you are leaving Canon TDD discipline.

## Framework Examples

Adapt syntax to whatever test framework the project uses. The methodology is
identical regardless of language or framework.

**TypeScript (Vitest/Jest):**
```typescript
describe('CartTotal', () => {
  it('returns zero when the cart is empty', () => {
    const cart = createCart();
    expect(cart.total()).toBe(0);
  });
});
```

**Python (pytest):**
```python
def test_cart_total_returns_zero_when_empty():
    cart = create_cart()
    assert cart.total() == 0
```

**Go:**
```go
func TestCartTotal_ReturnsZeroWhenEmpty(t *testing.T) {
    cart := NewCart()
    if got := cart.Total(); got != 0 {
        t.Errorf("Total() = %d, want 0", got)
    }
}
```

**C++ (Google Test):**
```cpp
TEST(CartTotalTest, ReturnsZeroWhenEmpty) {
    Cart cart;
    EXPECT_EQ(cart.total(), 0);
}
```

**C++ (Catch2):**
```cpp
TEST_CASE("Cart total returns zero when empty", "[cart]") {
    Cart cart;
    REQUIRE(cart.total() == 0);
}
```

Use whatever matches the project. Do not force a framework.

## Common TDD Anti-Patterns to Resist

| Anti-Pattern | What It Looks Like | What To Do Instead |
|---|---|---|
| Test-after | Writing implementation then tests | Always Red first |
| Assertion-free tests | Tests that call code but check nothing | Every test needs a real assertion |
| Multiple tests at once | Writing 5 tests before making any pass | One at a time, always |
| Gold-plating in Green | Building general solution for first test | Minimum to pass, generalize later |
| Refactoring in Red | "Cleaning up" while writing the test | Write the test, stop, run it |
| Refactoring in Green | Improving design while making it pass | Make it pass, then refactor separately |
| Implementation leakage | Test knows about internals it shouldn't | Test the interface, not the guts |
| Test-list skipping | Jumping straight to writing tests | Always start with behavioral analysis |
| Copy-paste expected values | Running code, copying output into assertion | Expected values come from understanding the behavior |
| **Deleting tests to pass** | Removing or skipping a failing test | Fix the implementation, never the test |
| **Weakening assertions** | Changing `toBe(42)` to `toBeTruthy()` | Keep the original assertion; it encodes the spec |
| **Return true to cheat** | `return true` to bypass real logic | Implement the actual behavior the test demands |
