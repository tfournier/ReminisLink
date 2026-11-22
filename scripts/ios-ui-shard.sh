#!/bin/bash
# Splits the app's UI test classes between the CI jobs ios-ui-1 and ios-ui-2
# (.github/workflows/ci.yml), each on its own Mac with one simulator:
#   scripts/ios-ui-shard.sh <n> <total>            -only-testing: arguments of shard n
#   scripts/ios-ui-shard.sh --classes <n> <total>  the class names of shard n
#
# The classes are the XCTestCase subclasses declared in
# ios/ReminisLinkUITests (RL_UITESTS_DIR overrides it, for the tests). Each
# class weighs its duration on the CI, in seconds (the table below), or 30 s
# per test method when the table does not know it (a new class). The
# heaviest class goes first (ties: by name) to the shard that weighs the
# least so far (ties: the lowest number): the same classes always give the
# same split. Update the table when a class grows or shrinks a lot; no class
# can be forgotten (scripts/test-release-scripts.sh checks that the shards
# together hold every class exactly once).
#
# Never empty: a shard without a class fails, since xcodebuild without
# -only-testing would run every test.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dir="${RL_UITESTS_DIR:-$root/ios/ReminisLinkUITests}"
target=ReminisLinkUITests

classes_only=0
if [ "${1:-}" = --classes ]; then
	classes_only=1
	shift
fi
usage() {
	echo "usage: scripts/ios-ui-shard.sh [--classes] <n> <total>  (1 <= n <= total)" >&2
	exit 2
}
[ "$#" = 2 ] || usage
n="$1"
total="$2"
[[ "$n" =~ ^[1-9][0-9]*$ && "$total" =~ ^[1-9][0-9]*$ ]] || usage
[ "$n" -le "$total" ] || usage
[ -d "$dir" ] || { echo "ios-ui-shard: no folder $dir" >&2; exit 1; }

# Seconds per class in the job "ios" of the green CI run 45055194565
# (2026-11-22, one simulator on macos-26, before the split).
weights="AddFileUITests=108 AppStoreScreenshotsUITests=1 ArtUITests=51"
weights+=" CheatsUITests=134 CompatibilityUITests=68 DemoModeUITests=70"
weights+=" DeviceBusyUITests=34 ErgonomicsUITests=229 EventsUITests=1"
weights+=" FilesUITests=97 MacPairingUITests=24 PairingUITests=155"
weights+=" PaksUITests=74 PatchUITests=120 SaveUITests=95"
weights+=" ScreenshotsUITests=114 StatesUITests=62 SyncUITests=73"
weights+=" TrashRestoreUITests=155 UniversalUITests=25"

# "<class> <test methods>" for each XCTestCase subclass, in file order.
found="$(find "$dir" -name '*.swift' -type f | LC_ALL=C sort | xargs awk '
	FNR == 1 { current = "" }
	/^[[:space:]]*(@[A-Za-z]+[[:space:]]+)*((public|internal|open|final)[[:space:]]+)*class[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]*:[[:space:]]*XCTestCase([^A-Za-z0-9_]|$)/ {
		line = $0
		sub(/^[[:space:]]*(@[A-Za-z]+[[:space:]]+)*((public|internal|open|final)[[:space:]]+)*class[[:space:]]+/, "", line)
		sub(/[^A-Za-z0-9_].*$/, "", line)
		current = line
		order[++count] = current
		tests[current] += 0
		next
	}
	/^[[:space:]]*(@MainActor[[:space:]]+)?func[[:space:]]+test[A-Za-z0-9_]*[[:space:]]*\(/ {
		if (current != "") tests[current]++
	}
	END { for (i = 1; i <= count; i++) print order[i], tests[order[i]] }')"
[ -n "$found" ] || { echo "ios-ui-shard: no XCTestCase class in $dir" >&2; exit 1; }
dup="$(cut -d' ' -f1 <<<"$found" | LC_ALL=C sort | uniq -d)"
[ -z "$dup" ] || { echo "ios-ui-shard: class declared twice: $dup" >&2; exit 1; }

shard="$(awk -v n="$n" -v total="$total" -v table="$weights" '
	BEGIN {
		pairs = split(table, rows, " ")
		for (i = 1; i <= pairs; i++) { split(rows[i], f, "="); known[f[1]] = f[2] + 0 }
	}
	{ name[NR] = $1; weight[NR] = ($1 in known) ? known[$1] : 30 * ($2 > 0 ? $2 : 1) }
	END {
		count = NR
		# Heaviest first, then by name: a stable order (insertion sort).
		for (i = 1; i <= count; i++) idx[i] = i
		for (i = 2; i <= count; i++) {
			j = i
			while (j > 1 && (weight[idx[j]] > weight[idx[j-1]] || \
				(weight[idx[j]] == weight[idx[j-1]] && name[idx[j]] < name[idx[j-1]]))) {
				t = idx[j]; idx[j] = idx[j-1]; idx[j-1] = t; j--
			}
		}
		for (s = 1; s <= total; s++) load[s] = 0
		for (i = 1; i <= count; i++) {
			best = 1
			for (s = 2; s <= total; s++) if (load[s] < load[best]) best = s
			load[best] += weight[idx[i]]
			if (best == n) print name[idx[i]]
		}
	}' <<<"$found" | LC_ALL=C sort)"
[ -n "$shard" ] || { echo "ios-ui-shard: shard $n of $total has no class" >&2; exit 1; }

if [ "$classes_only" = 1 ]; then
	printf '%s\n' "$shard"
else
	sed "s|^|-only-testing:$target/|" <<<"$shard"
fi
