#!/usr/bin/env bash
# Only an exact, nonempty PR diff can opt out of the full suite.
# Do not use branch names: base/head can move during force-push or base sync.
set -eu

mode=full
diff_file=""
cleanup() {
  if [ -n "$diff_file" ]; then rm -f "$diff_file"; fi
}
trap cleanup EXIT

if [[ "${EVENT_NAME:-}" == pull_request &&
      "${BASE_SHA:-}" =~ ^[0-9a-f]{40}$ &&
      "${HEAD_SHA:-}" =~ ^[0-9a-f]{40}$ ]] &&
    git cat-file -e "$BASE_SHA^{commit}" &&
    git cat-file -e "$HEAD_SHA^{commit}"; then
  diff_file="$(mktemp)" || diff_file=""
  # Disable rename detection so both the old and new path are classified.
  # A failed diff may leave partial output; never classify that output.
  if [ -n "$diff_file" ] &&
      git diff --no-ext-diff --no-textconv --no-renames --name-only -z \
        "$BASE_SHA" "$HEAD_SHA" -- > "$diff_file"; then
    count=0
    docs=true
    path=""
    while IFS= read -r -d '' path; do
      count=$((count + 1))
      case "$path" in
        ''|/*|./*|../*|*/../*|*/./*|*//*) docs=false ;;
        Plans/*|docs/*) ;;
        *.md) [[ "$path" != */* ]] || docs=false ;;
        *) docs=false ;;
      esac
    done < "$diff_file"
    # An unterminated record or an empty diff is uncertainty, not docs-only.
    if [ "$docs" = true ] && [ "$count" -gt 0 ] && [ -z "$path" ]; then
      mode=docs
    fi
  fi
fi

printf 'mode=%s\n' "$mode" >> "$GITHUB_OUTPUT"
printf 'CI diff classification: %s\n' "$mode"
