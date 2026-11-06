#!/bin/sh
# Refuses a decision board verdict ID in text. A verdict ID exists only in one machine's
# store, so a commit, a pull request or a document names the card reference (#536, #589).
# Reads the text on standard input, prints each line that cites a verdict ID, in any case,
# and exits 1.
found=$(grep -niE 'verdicts?( id)? #?[0-9]+')
if [ -n "$found" ]; then
  printf '%s\n' "$found" >&2
  echo "Name a decision board verdict by its card reference, not by its verdict ID (#536)." >&2
  exit 1
fi
