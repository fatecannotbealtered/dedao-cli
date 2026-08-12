#!/bin/sh
# Project-specific clean check: no stray tracked files at the repository root.
#
# Debug captures, scraped assets, and one-off reports land at the root first,
# so the root is guarded by an explicit allowlist of the repo skeleton; a new
# legitimate root file is added here deliberately, in the same change that
# introduces it. Subdirectories are governed by review and .gitignore.
#
# POSIX sh only; needs nothing beyond git itself.
set -eu

offending=$(git ls-files | while IFS= read -r file; do
  case "$file" in
    */*) continue ;;
  esac
  case "$file" in
    .gitattributes | .gitignore | .golangci.yml | .goreleaser.yml | .npmrc) ;;
    AGENTS.md | AGENTS_zh.md | CHANGELOG.md | LICENSE | Makefile) ;;
    CODE_OF_CONDUCT.md | CODE_OF_CONDUCT_zh.md | CONTRIBUTING.md | CONTRIBUTING_zh.md) ;;
    NOTICE.md | NOTICE_zh.md | README.md | README_zh.md | SECURITY.md | SECURITY_zh.md) ;;
    changelog.go | changelog_test.go | contract.go | go.mod | go.sum) ;;
    package.json | package-lock.json) ;;
    *) printf '%s\n' "$file" ;;
  esac
done)

if [ -n "$offending" ]; then
  echo "check-clean: tracked root-level files outside the allowlist:" >&2
  printf '%s\n' "$offending" | while IFS= read -r file; do
    printf '  %s\n' "$file" >&2
  done
  echo "check-clean: move them into a directory, untrack them, or extend scripts/check-clean.sh" >&2
  exit 1
fi
echo "check-clean: root-level tracked files all match the allowlist."
