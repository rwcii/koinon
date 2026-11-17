#!/bin/sh
# Refuses a decision board verdict ID in text. A verdict ID exists only in one machine's
# store, so a commit, a pull request or a document cites the repository record that holds the
# decision (#536, #589, #723).
# Reads the text on standard input, prints each line that cites a verdict ID, in any case,
# and exits 1.
found=$(grep -niE 'verdicts?( id)? #?[0-9]+')
if [ -n "$found" ]; then
  printf '%s\n' "$found" >&2
  echo "Cite the repository record that holds the decision, not its verdict ID (#536, #723)." >&2
  exit 1
fi
