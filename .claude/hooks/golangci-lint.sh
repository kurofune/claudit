#!/usr/bin/env bash
# PostToolUse hook: run golangci-lint on the package containing the
# edited Go file and block on any issue. The codebase is kept at zero
# lint issues, so every flag is something Claude just introduced.
#
# Exit 2 with stderr = blocking error fed back to the model.
set -u

input=$(cat)
file=$(printf '%s' "$input" | jq -r '.tool_response.filePath // .tool_input.file_path // empty')

case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac

[ -f "$file" ] || exit 0

dir=$(dirname "$file")

if ! command -v golangci-lint >/dev/null 2>&1; then
  exit 0
fi

out=$(golangci-lint run --max-same-issues=0 --max-issues-per-linter=0 "$dir" 2>&1)
rc=$?

if [ $rc -ne 0 ]; then
  printf 'golangci-lint reported issues in %s:\n%s\n' "$file" "$out" >&2
  exit 2
fi
exit 0
