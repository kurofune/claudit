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
| `subagent.jsonl` | rollout `01a19217-0c60-71a2-ab13-917397bf7ed1` (`thread_source: subagent`) | `session_id` is the parent's; the thread's own uuid is `id` and the file name. Usage numbers come from another real record. |
| `guardian.jsonl` | rollout `01a18784-eaa5-7850-b7db-776a88eb52ee` (`thread_source: guardian_review`, model `codex-auto-review`) | Usage numbers are synthetic. |
