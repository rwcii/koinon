#!/usr/bin/env bash
# Write the Homebrew bottles of one release: homebrew-bottles.sh TAG DIR.
# DIR holds the four release binaries. Each bottle, DIR/koinon-VERSION.BOTTLE_TAG.bottle.tar.gz,
# holds koinon/VERSION/bin/koinon. Homebrew pours a bottle without building, so on Apple Silicon
# and Linux it needs no developer tools (#384); Homebrew requires them on Intel Macs regardless.
# A macOS bottle carries the oldest supported macOS (12, Go's minimum), which Homebrew also uses
# on later versions; a Linux bottle tag must match exactly.
set -euo pipefail
if [[ $# -ne 2 ]]; then
  echo "usage: $0 TAG DIR" >&2
  exit 2
fi
tag=$1
dir=$2
if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "tag must be vMAJOR.MINOR.PATCH: $tag" >&2
  exit 2
fi
version=${tag#v}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for pair in darwin-arm64:arm64_monterey darwin-amd64:monterey linux-arm64:arm64_linux linux-amd64:x86_64_linux; do
  binary="$dir/koinon-${pair%%:*}"
  if [[ ! -f $binary ]]; then
    echo "no binary $binary" >&2
    exit 1
  fi
  rm -rf "$work/koinon"
  mkdir -p "$work/koinon/$version/bin"
  cp "$binary" "$work/koinon/$version/bin/koinon"
  chmod 0755 "$work/koinon/$version/bin/koinon"
  tar -C "$work" -czf "$dir/koinon-$version.${pair#*:}.bottle.tar.gz" "koinon/$version"
done
