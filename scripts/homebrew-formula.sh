#!/usr/bin/env bash
# Print the Homebrew formula for one release: homebrew-formula.sh TAG SHA256SUMS.
# The release workflow writes the output to Formula/koinon.rb in rwcii/homebrew-koinon.
# Homebrew reads the version from the release URL; an explicit version fails brew audit --strict.
set -euo pipefail
if [[ $# -ne 2 ]]; then
  echo "usage: $0 TAG SHA256SUMS" >&2
  exit 2
fi
tag=$1
sums=$2
if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "tag must be vMAJOR.MINOR.PATCH: $tag" >&2
  exit 2
fi
sum() {
  local hash
  hash=$(awk -v f="$1" '$2 == f || $2 == "*" f { print $1 }' "$sums")
  if [[ ! $hash =~ ^[0-9a-f]{64}$ ]]; then
    echo "no sha256 for $1 in $sums" >&2
    exit 1
  fi
  printf '%s' "$hash"
}
# Each assignment stops the script on a missing sum; a substitution inside the heredoc would not.
darwin_arm=$(sum koinon-darwin-arm64)
darwin_intel=$(sum koinon-darwin-amd64)
linux_arm=$(sum koinon-linux-arm64)
linux_intel=$(sum koinon-linux-amd64)
base="https://github.com/rwcii/koinon/releases/download/$tag"
cat <<RUBY
class Koinon < Formula
  desc "Shared coordination and memory for local agent sessions"
  homepage "https://github.com/rwcii/koinon"
  license "MIT"

  on_macos do
    on_arm do
      url "$base/koinon-darwin-arm64"
      sha256 "$darwin_arm"
    end
    on_intel do
      url "$base/koinon-darwin-amd64"
      sha256 "$darwin_intel"
    end
  end

  on_linux do
    on_arm do
      url "$base/koinon-linux-arm64"
      sha256 "$linux_arm"
    end
    on_intel do
      url "$base/koinon-linux-amd64"
      sha256 "$linux_intel"
    end
  end

  def install
    bin.install Dir["koinon-*"].first => "koinon"
  end

  def caveats
    <<~EOS
      Install the daemon and set up your agents:
        koinon install --agent claude --agent codex
      After each brew upgrade, run koinon install again. It copies the new binary
      into its own prefix and restarts the daemon.
    EOS
  end

  test do
    assert_match "\"version\": \"v#{version}\"", shell_output("#{bin}/koinon version")
  end
end
RUBY
