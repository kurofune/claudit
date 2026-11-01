# Codex rollout fixtures

Hand-anonymized from real Codex CLI 0.165–0.166 rollouts under
`~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`, record shapes observed
2026-10-31. Every cwd is `/home/u/proj`; prompt, output, instruction and git text
is replaced; ids, timestamps, token counts and record structure are kept. No raw
rollout is committed.

| File | From | Notes |
|---|---|---|
| `basic.jsonl` | rollout `01a1708b-04a3-75a1-a07c-92bbb16be66f` (2026-10-25, `gpt-6-astra`, `codex_exec`) | Second `token_usage_record` is the real 48273 input / 38400 cached record. The first record's `cache_write_input_tokens` (512) and `reasoning_output_tokens` (23) are synthetic: every real record seen carries `cache_write_input_tokens: 0`, and the test needs a non-zero value to check the mapping. |
| `tools.jsonl` | rollout `01a19461-aed4-78a0-bad1-efc7859dd26a` (2026-10-31, `exec` custom tool) plus the `function_call exec_command` / `function_call_output` shape of a 2026-05-30 rollout | One output reports exit code 0 (the JSON `exit_code` form inside an `exec` result), one exit code 1 (the `Exit code: N` header form). |
| `subagent.jsonl` | rollout `01a19217-0c60-71a2-ab13-917397bf7ed1` (`thread_source: subagent`) | `session_id` is the parent's; the thread's own uuid is `id` and the file name. Usage numbers come from another real record. Line 3 is the subagent's injected-only user message (AGENTS.md + environment_context), the shape every real subagent rollout opens with. |
| `parent.jsonl` | synthetic, shaped like a `codex-tui` user thread | Stands in for `subagent.jsonl`'s parent thread `01a15d72-75c8-7ec1-bff4-4d487e6488ad`: three real prompts at 13:05, 13:12:20 and 13:30, around the subagent's first turn at 13:12:31.512. The first prompt mixes injected items with a real one. Usage numbers are synthetic. |
| `prompts.jsonl` | message shapes of rollout `01a190da-3a10-7c41-9d2e-6f0b1c2d3e4f` (2026-10-31, `codex_exec`), ids kept, text replaced | A `codex_exec` thread: the first user message holds only AGENTS.md + environment_context, the real ask ("fix the login bug") is the next message, and a second prompt follows two turns later. The turn before the first real prompt (line 4) is synthetic, so the test sees a turn with no prompt. Usage numbers are synthetic. |
| `guardian.jsonl` | rollout `01a18784-eaa5-7850-b7db-776a88eb52ee` (`thread_source: guardian_review`, model `codex-auto-review`) | Usage numbers are synthetic. |
| `legacy.jsonl` | rollout `019e77f8-3d08-7371-bc87-6e200272fbeb` (2026-05-30, `gpt-5.5`, Codex Desktop 0.130.0-alpha.5) | No `token_usage_record`; usage comes from `event_msg` `token_count`. `session_meta` drops `base_instructions`, `dynamic_tools` and `git`; `turn_context` and `thread_settings_applied` keep only scalar settings; commands keep their shape with file names replaced. Two synthetic edits: line 44 repeats line 43's reading with `used_percent` 11.0 (Codex repeats a reading when only `rate_limits` change; this rollout has none), and line 33's `cache_write_input_tokens` is 256 instead of 0 so the mapping test sees a non-zero value. |
