#!/bin/bash
# The App Store "What's New" texts, from ios/CHANGELOG.md (milestone M17).
#
# The changelog has one "## <section>" per version ("## Unreleased" first
# while changes wait for a release, then "## X.Y.Z", newest first), each with
# a "### English" and a "### Français" part. Lines starting with "- " become
# "• " in the App Store text.
#
#   release_notes.sh top                     the first section's name
#   release_notes.sh has <section>           exit 0 when the section exists
#   release_notes.sh print <section> <lang>  the text (lang: en-US or fr-FR)
#   release_notes.sh sync                    write AppStore/<lang>/release_notes.txt from the first section
#   release_notes.sh check                   exit 1 when those files are not in sync (CI)
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
changelog="${RL_CHANGELOG:-$dir/CHANGELOG.md}"
appstore="${RL_APPSTORE_DIR:-$dir/AppStore}"
langs="en-US fr-FR"
# App Store Connect limit of "What's New in This Version".
max_chars=4000

die() {
	echo "release_notes.sh: $*" >&2
	exit 1
}

[ -f "$changelog" ] || die "no changelog at $changelog"

top() {
	sed -n 's/^## \(.*[^ ]\) *$/\1/p' "$changelog" | head -n 1
}

has() {
	grep -Fqx "## $1" "$changelog"
}

heading() {
	case "$1" in
	en-US) echo "### English" ;;
	fr-FR) echo "### Français" ;;
	*) die "unknown language $1 (en-US or fr-FR)" ;;
	esac
}

# The part of one language in one section, blank lines around trimmed.
print() {
	local section="$1" lang="$2" text
	has "$section" || die "no '## $section' section in $changelog"
	text="$(awk -v section="## $section" -v part="$(heading "$lang")" '
		/^## / { in_section = ($0 == section); in_part = 0; next }
		in_section && /^### / { in_part = ($0 == part); next }
		in_section && in_part { print }
	' "$changelog" | sed 's/^- /• /' | sed -e '/./,$!d')"
	# Trailing blank lines: the command substitution drops them.
	[ -n "$text" ] || die "'## $section' has no '$(heading "$lang")' text"
	printf '%s\n' "$text"
}

length_ok() {
	local chars
	chars="$(printf '%s' "$1" | LC_ALL=en_US.UTF-8 wc -m | tr -d ' ')"
	[ "$chars" -le "$max_chars" ] || die "$2: $chars characters (App Store limit: $max_chars)"
}

case "${1:-}" in
top)
	top
	;;
has)
	[ $# -eq 2 ] || die "usage: release_notes.sh has <section>"
	has "$2"
	;;
print)
	[ $# -eq 3 ] || die "usage: release_notes.sh print <section> <en-US|fr-FR>"
	print "$2" "$3"
	;;
sync | check)
	section="$(top)"
	[ -n "$section" ] || die "no '## <version>' section in $changelog"
	status=0
	for lang in $langs; do
		text="$(print "$section" "$lang")"
		length_ok "$text" "$lang"
		file="$appstore/$lang/release_notes.txt"
		if [ "$1" = sync ]; then
			printf '%s\n' "$text" >"$file.tmp"
			mv "$file.tmp" "$file"
		elif [ "$(cat "$file" 2>/dev/null)" != "$text" ]; then
			echo "$file is not the '$section' section of ios/CHANGELOG.md: run ios/ci_scripts/release_notes.sh sync" >&2
			status=1
		fi
	done
	exit "$status"
	;;
*)
	die "usage: release_notes.sh top | has <section> | print <section> <lang> | sync | check"
	;;
esac
