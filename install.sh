#!/bin/sh
# Install the CLI only. Project registration requires a release with vibe-init.
set -eu
umask 077
fail() { printf '%s\n' "onebase: $*" >&2; exit 1; }
project=''
agents=''
dry_run=false
install_dir=${HOME:?HOME is required}/.onebase/bin
release_api=https://api.github.com/repos/ivanarama/onebase/releases/latest
while [ "$#" -gt 0 ]; do
    case "$1" in
        --project|--agents|--install-dir|--release-api)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "Missing value for $1"
            case "$1" in
                --project) project=$2 ;;
                --agents) agents=$2 ;;
                --install-dir) install_dir=$2 ;;
                --release-api) release_api=$2 ;;
            esac
            shift 2 ;;
        --dry-run) dry_run=true; shift ;;
        --help)
            printf '%s\n' 'Usage: sh install.sh [--project DIR] [--agents claude,cursor,opencode,zcode] [--dry-run] [--install-dir DIR] [--release-api URL]'
            exit 0 ;;
        *) fail "Unknown argument: $1" ;;
    esac
done
[ -z "$agents" ] || [ -n "$project" ] || fail '--agents requires --project'
case "$agents" in ,*|*,|*,,*) fail 'Empty agent name' ;; esac
if [ -n "$agents" ]; then
    remaining=$agents
    while :; do
        agent=${remaining%%,*}
        case "$agent" in claude|cursor|opencode|zcode) ;; *) fail "Unknown agent: $agent" ;; esac
        case "$remaining" in *,*) remaining=${remaining#*,} ;; *) break ;; esac
    done
fi
case "$install_dir" in /*) ;; *) install_dir=$PWD/$install_dir ;; esac
case "$project" in ''|/*) ;; *) project=$PWD/$project ;; esac
if "$dry_run"; then
    printf 'Dry-run: keep existing onebase, otherwise install CLI at %s/onebase\n' "$install_dir"
    [ -z "$project" ] || printf 'Dry-run: vibe-init project=%s agents=%s (auto if empty)\n' "$project" "$agents"
    exit 0
fi
[ ! -L "$install_dir" ] || fail 'Install directory is a symlink'
[ ! -L "$install_dir/onebase" ] || fail 'Installed binary is a symlink'
exe=''
if [ -e "$install_dir/onebase" ]; then
    [ -f "$install_dir/onebase" ] && [ -x "$install_dir/onebase" ] || fail 'Installed binary is not executable'
    exe=$install_dir/onebase
elif command -v onebase >/dev/null 2>&1; then
    exe=$(command -v onebase)
    case "$exe" in /*) ;; *) exe=$PWD/$exe ;; esac
    [ -f "$exe" ] && [ -x "$exe" ] && [ ! -L "$exe" ] || fail 'PATH onebase is not a regular executable'
fi
check_binary() {
    "$1" --version || fail 'Binary failed --version; existing installation preserved'
    if [ -n "$project" ]; then
        "$1" vibe-init --help >/dev/null || fail 'This binary has no usable vibe-init. Update explicitly to a supporting release.'
    fi
}
staging=''
publish=''
cleanup() {
    [ -z "$publish" ] || rm -rf "$publish"
    [ -z "$staging" ] || rm -rf "$staging"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if [ -n "$exe" ]; then
    printf 'Keeping existing binary: %s\n' "$exe"
    check_binary "$exe"
else
    for tool in curl jq tar mktemp; do command -v "$tool" >/dev/null 2>&1 || fail "Required tool: $tool"; done
    if command -v sha256sum >/dev/null 2>&1; then hash_tool=sha256sum
    elif command -v shasum >/dev/null 2>&1; then hash_tool=shasum
    else fail 'Required tool: sha256sum or shasum'; fi
    case $(uname -s) in Linux) goos=linux ;; Darwin) goos=darwin ;; *) fail 'Unsupported operating system' ;; esac
    case $(uname -m) in x86_64|amd64) goarch=amd64 ;; aarch64|arm64) goarch=arm64 ;; *) fail 'Unsupported architecture' ;; esac
    bundle=onebase-$goos-$goarch
    asset=$bundle.tar.gz
    staging=$(mktemp -d "${TMPDIR:-/tmp}/onebase-install.XXXXXXXX")
    download() { curl --proto =https,http --proto-redir =https,http --fail --location --silent --show-error --connect-timeout 15 --max-time 120 "$1" -o "$2"; }
    download "$release_api" "$staging/release.json"
    asset_url() {
        jq -er --arg name "$1" '[.assets[] | select(.name == $name)] | if length == 1 then .[0].browser_download_url | select(type == "string" and length > 0) else error("Missing or duplicate exact asset") end' "$staging/release.json"
    }
    archive_url=$(asset_url "$asset")
    sum_url=$(asset_url "$asset.sha256")
    download "$archive_url" "$staging/$asset"
    download "$sum_url" "$staging/sum"
    expected=$(awk -v name="$asset" 'NF == 2 && $2 == name { print $1 }' "$staging/sum")
    [ "${#expected}" -eq 64 ] || fail 'Invalid checksum record'
    case "$expected" in *[!0-9a-fA-F]*) fail 'Invalid SHA-256' ;; esac
    if [ "$hash_tool" = shasum ]; then actual=$(shasum -a 256 "$staging/$asset"); else actual=$(sha256sum "$staging/$asset"); fi
    actual=${actual%% *}
    expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
    [ "$actual" = "$expected" ] || fail 'SHA-256 mismatch'
    # Stream exactly one named member: archive-controlled paths are never
    # extracted onto disk, and unrelated entries cannot write files.
    tar -tzf "$staging/$asset" > "$staging/members"
    count=$(awk -v name="$bundle/onebase" '$0 == name { n++ } END { print n+0 }' "$staging/members")
    [ "$count" -eq 1 ] || fail 'Archive must contain exactly one platform CLI'
    tar -xOzf "$staging/$asset" "$bundle/onebase" > "$staging/onebase"
    [ -s "$staging/onebase" ] || fail 'Empty CLI in archive'
    chmod 700 "$staging/onebase"
    check_binary "$staging/onebase"
    mkdir -p "$install_dir"
    [ ! -L "$install_dir" ] || fail 'Install directory became a symlink'
    publish=$(mktemp -d "$install_dir/.onebase-publish.XXXXXXXX")
    cp "$staging/onebase" "$publish/onebase"
    chmod 700 "$publish/onebase"
    # Publish a complete inode without replacing a concurrent installation.
    ln "$publish/onebase" "$install_dir/" || fail 'Binary appeared during installation; no replacement performed'
    exe=$install_dir/onebase
    printf 'Installed CLI: %s\n' "$exe"
fi
if [ -n "$project" ]; then
    set -- vibe-init "$project"
    [ -z "$agents" ] || set -- "$@" --agents "$agents"
    "$exe" "$@"
else
    printf 'Run: "%s" start\n' "$exe"
fi
