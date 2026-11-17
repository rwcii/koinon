#!/bin/sh
# Install the skills of one Koinon release on a computer with no Koinon (#732; PDR 008,
# Decision 2): install-skills.sh VERSION, where VERSION is a release tag, vMAJOR.MINOR.PATCH.
#
# It downloads koinon-skills.tar.gz and SHA256SUMS of that release, checks the archive's hash
# and every entry, and writes each skill as koinon-NAME/SKILL.md, with only its name line
# changed, into the user skill roots that koinon install uses (core.SkillRoots): Claude Code's
# configuration root ($CLAUDE_CONFIG_DIR, else ~/.claude), Codex's ~/.agents, and OpenCode's
# ($XDG_CONFIG_HOME/opencode, else ~/.config/opencode), agy's (~/.gemini/config) and Grok's
# ($GROK_HOME, else ~/.grok) configuration folders when they exist. koinon-guide needs the
# Koinon binary and is not installed. A symbolic link at the entry file, the entry folder, the
# skills folder or the configuration folder is kept and reported; a linked folder above the
# configuration folder, such as a linked home, is written through. (koinon install checks only
# the entry file and the entry folder.) A later koinon install replaces these copies with its
# own entries. KOINON_SKILLS_BASE replaces the release download URL.
set -eu

usage() {
	echo "usage: $0 VERSION (a release tag, vMAJOR.MINOR.PATCH)" >&2
	exit 2
}
[ $# -eq 1 ] || usage
version=$1
printf '%s\n' "$version" | awk '!/^v[0-9]+\.[0-9]+\.[0-9]+$/ { exit 1 }' || usage
: "${HOME:?HOME is not set}"
base=${KOINON_SKILLS_BASE:-https://github.com/rwcii/koinon/releases/download/$version}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
umask 077

fetch() {
	if ! curl -fsSL -o "$work/$1" "$base/$1"; then
		echo "cannot download $base/$1" >&2
		exit 1
	fi
}
fetch koinon-skills.tar.gz
fetch SHA256SUMS

# The archive's line in SHA256SUMS: exactly one, with a 64-digit lower-case hash.
want=$(awk '$2 == "koinon-skills.tar.gz" || $2 == "*koinon-skills.tar.gz" { print $1; n++ } END { if (n != 1) exit 1 }' "$work/SHA256SUMS") || {
	echo "SHA256SUMS of $version has no single line for koinon-skills.tar.gz" >&2
	exit 1
}
printf '%s\n' "$want" | awk '!/^[0-9a-f]{64}$/ { exit 1 }' || {
	echo "SHA256SUMS of $version has a malformed hash for koinon-skills.tar.gz" >&2
	exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$work/koinon-skills.tar.gz" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$work/koinon-skills.tar.gz" | awk '{ print $1 }')
fi
if [ "$got" != "$want" ]; then
	echo "koinon-skills.tar.gz of $version does not match SHA256SUMS" >&2
	exit 1
fi

# Every entry is NAME/ or NAME/SKILL.md, a folder or a regular file, before anything is
# extracted. The verbose listing gives each entry's type in its first character; -P lists
# absolute and parent names as they are, so that they are refused.
tar -tPzf "$work/koinon-skills.tar.gz" > "$work/names" 2>/dev/null || {
	echo "koinon-skills.tar.gz of $version cannot be read" >&2
	exit 1
}
tar -tvPzf "$work/koinon-skills.tar.gz" 2>/dev/null | cut -c1 > "$work/types"
if ! awk 'NR == FNR { type[FNR] = $0; n = FNR; next }
	{ t = type[FNR] }
	t == "-" && $0 ~ /^[a-z0-9]+(-[a-z0-9]+)*\/SKILL\.md$/ { files++; next }
	t == "d" && $0 ~ /^[a-z0-9]+(-[a-z0-9]+)*\/$/ { next }
	{ bad = 1 }
	END { if (bad || files == 0 || FNR != n) exit 1 }' "$work/types" "$work/names"; then
	echo "koinon-skills.tar.gz of $version has an entry that is not NAME/SKILL.md" >&2
	exit 1
fi
mkdir "$work/x"
tar -xzf "$work/koinon-skills.tar.gz" -C "$work/x"

# Each skill's name line names its folder; the copy changes only that line.
mkdir "$work/out"
awk -F/ '/\/SKILL\.md$/ { print $1 }' "$work/names" | sort -u > "$work/skills"
while read -r name; do
	src="$work/x/$name/SKILL.md"
	if [ ! -f "$src" ] || [ -h "$src" ] || [ "$(sed -n 2p "$src")" != "name: $name" ] || [ "$(sed -n 1p "$src")" != "---" ]; then
		echo "the $name skill of $version has no name line for $name" >&2
		exit 1
	fi
	[ "$name" = koinon-guide ] && continue
	case $name in
	koinon-*) entry=$name ;;
	*) entry=koinon-$name ;;
	esac
	mkdir "$work/out/$entry"
	sed "2s/^name: .*/name: $entry/" "$src" > "$work/out/$entry/SKILL.md"
done < "$work/skills"

# The roots: FAMILY CONFIGURATION-FOLDER REQUIRED.
claude=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
opencode=${XDG_CONFIG_HOME:-$HOME/.config}/opencode
grok=${GROK_HOME:-$HOME/.grok}

linked() {
	for p in "$@"; do
		[ -h "$p" ] && return 0
	done
	return 1
}

install_root() {
	family=$1 config=$2 required=$3
	if [ "$required" = no ] && [ ! -d "$config" ]; then
		return 0
	fi
	for dir in "$work/out"/*/; do
		entry=$(basename "$dir")
		target="$config/skills/$entry/SKILL.md"
		if linked "$config" "$config/skills" "$config/skills/$entry" "$target"; then
			echo "kept      $family $target"
			continue
		fi
		if [ -f "$target" ] && cmp -s "$dir/SKILL.md" "$target"; then
			echo "unchanged $family $target"
			continue
		fi
		result=created
		[ -e "$target" ] && result=replaced
		mkdir -p "$config/skills/$entry"
		tmp="$config/skills/$entry/.SKILL.md.$$"
		cp "$dir/SKILL.md" "$tmp"
		chmod 0644 "$tmp"
		mv -f "$tmp" "$target"
		printf '%-9s %s %s\n' "$result" "$family" "$target"
	done
}

install_root claude "$claude" yes
install_root codex "$HOME/.agents" yes
install_root opencode "$opencode" no
install_root agy "$HOME/.gemini/config" no
install_root grok "$grok" no
