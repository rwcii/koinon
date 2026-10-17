#!/usr/bin/env bash
# Print the Homebrew formula for one release: homebrew-formula.sh TAG SHA256SUMS [ROOT_URL].
# The release workflow writes the output to Formula/koinon.rb in rwcii/homebrew-koinon.
# Homebrew reads the version from the release URL; an explicit version fails brew audit --strict.
# When SHA256SUMS lists the bottles of homebrew-bottles.sh, the formula pours them from ROOT_URL,
# by default the release's download URL.
set -euo pipefail
if [[ $# -ne 2 && $# -ne 3 ]]; then
  echo "usage: $0 TAG SHA256SUMS [ROOT_URL]" >&2
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
# The bottle block: all four bottles, or none.
bottles=(arm64_monterey monterey arm64_linux x86_64_linux)
listed=0
for b in "${bottles[@]}"; do
  if awk -v f="koinon-${tag#v}.$b.bottle.tar.gz" '$2 == f || $2 == "*" f { found = 1 } END { exit !found }' "$sums"; then
    listed=$((listed + 1))
  fi
done
bottle=""
if [[ $listed -eq ${#bottles[@]} ]]; then
  bottle="  bottle do
    root_url \"${3:-$base}\""
  for b in "${bottles[@]}"; do
    hash=$(sum "koinon-${tag#v}.$b.bottle.tar.gz")
    bottle+="
    sha256 cellar: :any_skip_relocation, $b: \"$hash\""
  done
  bottle+="
  end

"
elif [[ $listed -ne 0 ]]; then
  echo "$sums lists $listed of the ${#bottles[@]} bottles" >&2
  exit 1
fi
cat <<RUBY
class Koinon < Formula
  desc "Shared coordination and memory for local agent sessions"
  homepage "https://github.com/rwcii/koinon"
  license "MIT"

${bottle}  on_macos do
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
      Install the daemon and set up your agents with this formula's binary:
        #{opt_bin}/koinon install --agent claude --agent codex
      The install links its binary to #{opt_bin}/koinon. To upgrade, run brew update,
      then brew upgrade koinon: the daemon restarts on the new version. An installation
      made by v0.3.0 or earlier has a copy instead; after this upgrade, run
      #{opt_bin}/koinon install once. A macOS --system install keeps a copy; upgrade
      it with #{opt_bin}/koinon install --system.
    EOS
  end

  test do
    assert_match "\"version\": \"v#{version}\"", shell_output("#{bin}/koinon version")
  end
end
RUBY
