#!/usr/bin/env sh
# Runs the CI staticcheck job before a push. Bypass: git push --no-verify.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
base=$(git rev-parse --verify -q '@{push}' 2>/dev/null || git rev-parse --verify -q origin/main)
if [ -n "$base" ] && ! git diff --name-only "$base" HEAD -- '*.go' go.mod go.sum | /usr/bin/grep -q .; then
  exit 0
fi
echo "pre-push: staticcheck ./... (same as ci / staticcheck)" >&2
if ! go run honnef.co/go/tools/cmd/staticcheck@latest ./...; then
  echo "pre-push: staticcheck failed; fix it or push with --no-verify" >&2
  exit 1
fi
