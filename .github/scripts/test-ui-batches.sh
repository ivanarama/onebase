#!/usr/bin/env bash
# Keep the complete UI suite within the ordinary per-process Go test deadline.
# Windows exceeded the package-wide 10m budget while starting another short test.
# Every discovered test, example and fuzz seed runs once; benchmarks stay opt-in.
set -euo pipefail

list_file="$(mktemp)"
trap 'rm -f "$list_file"' EXIT
go test -list . ./internal/ui > "$list_file"

names=()
while IFS= read -r name; do
  name="${name%$'\r'}"
  case "$name" in
    Test*|Example*|Fuzz*) names+=("$name") ;;
  esac
done < "$list_file"
if [ "${#names[@]}" -eq 0 ]; then
  echo "UI test discovery returned no runnable names" >&2
  exit 1
fi

batch_size=128
for ((start=0; start<${#names[@]}; start+=batch_size)); do
  pattern=""
  count=0
  for ((i=start; i<start+batch_size && i<${#names[@]}; i++)); do
    # Go top-level test names are identifiers, so they contain no regexp syntax.
    pattern="${pattern:+$pattern|}${names[i]}"
    count=$((count+1))
  done
  echo "UI batch $((start/batch_size+1)): $count of ${#names[@]} discovered names"
  go test -json -count=1 -timeout=10m -run "^($pattern)$" ./internal/ui
done
