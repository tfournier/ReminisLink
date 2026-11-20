#!/usr/bin/env bash
# Runs the real UI tests (the *AgainstHeadlessPak tests of
# ios/ReminisLinkUITests, docs/specs/08 §3) one at a time against the
# headless pak. A pairing link is a single-use secret, so each test gets a
# pak started for it alone, with --pairing-open on a fresh copy of
# testdata/sdcard, and the link that pak prints, passed in the variable
# the test reads (TEST_RUNNER_RL_UITEST_PAIR_URL[_<NAME>], ios/README.md).
# The pak is stopped (SIGTERM, by PID) after each test.
#
# The script also plays the part of the person running the tests:
#   - STATES then RESTORED share one card: after STATES, the pak is
#     stopped, slot 1 of Starlight Courier is changed by hand, and the pak
#     is started again on the same card (M5 acceptance, ios/README.md);
#   - CLOSING: SIGTERM to the pak a few seconds after the app paired;
#   - EVENTS_QUIT: SIGTERM to the pak once the test is ready (ready and
#     trigger files, so the test measures the delay);
#   - EVENTS_LIBRARY: a second client deletes "Lantern Keeper [b1]" once
#     the test is ready (scripts/smoke-events.sh UI_DELETE=1).
#
# Usage:
#   scripts/run-headless-uitests.sh [NAME...]
#       NAME is the suffix of the link variable (PAIRING, ADD_FILE, SAVE,
#       STATES, RESTORED, CHEATS, PATCH, ART, SYNC, LIBRARY, CLOSING,
#       EVENTS_QUIT, EVENTS_LIBRARY, PAKS) or the test method name; no
#       NAME runs them all, in that order. RESTORED brings STATES along.
#   scripts/run-headless-uitests.sh --list
#       lists the tests.
# Environment:
#   DESTINATION   xcodebuild destination (default
#                 'platform=iOS Simulator,name=iPhone 16')
#   DERIVED_DATA  xcodebuild -derivedDataPath (default build/uitests-dd)
#   SKIP_BUILD=1  reuse the last build-for-testing of DERIVED_DATA
#   KEEP_LOGS=1   keep the work folder (pak output, xcodebuild logs)
#
# Each test prints PASS, FAIL or SKIP. PASS needs the XCTest line saying
# that this very method passed (an empty run fails, e.g. a renamed method
# with SKIP_BUILD=1); a test that skipped itself (e.g. a link not
# received) counts as a failure. A test that never started (no "started"
# line and the app never paired) is tried once more with a new pak. Exits
# 1 if any test did not pass. Pairing links only go through files and
# environment variables, never through process arguments nor a shell
# trace, and are never printed. Works with the bash 3.2 of macOS.
# The pairing links, secrets and keys must never reach a shell trace:
# tracing (bash -x, set -x, SHELLOPTS) is turned off for the whole run.
{ set +x; } 2>/dev/null
set -euo pipefail
cd "$(dirname "$0")/.."

# name|test (Class/method)|variable
TESTS="PAIRING|PairingUITests/testPairingLinkThenGameSheetAgainstHeadlessPak|RL_UITEST_PAIR_URL
ADD_FILE|AddFileUITests/testAddFileThenDeleteAgainstHeadlessPak|RL_UITEST_PAIR_URL
SAVE|SaveUITests/testBackupCopiesAgainstHeadlessPak|RL_UITEST_PAIR_URL_SAVE
STATES|StatesUITests/testLocksAgainstHeadlessPak|RL_UITEST_PAIR_URL_STATES
RESTORED|StatesUITests/testRestoredBadgeAgainstHeadlessPak|RL_UITEST_PAIR_URL_RESTORED
CHEATS|CheatsUITests/testCheatsAgainstHeadlessPak|RL_UITEST_PAIR_URL_CHEATS
PATCH|PatchUITests/testPatchAgainstHeadlessPak|RL_UITEST_PAIR_URL_PATCH
ART|ArtUITests/testChangeThenRemoveTheImageAgainstHeadlessPak|RL_UITEST_PAIR_URL_ART
SYNC|SyncUITests/testSyncAndRestoreAgainstHeadlessPak|RL_UITEST_PAIR_URL_SYNC
LIBRARY|ErgonomicsUITests/testLibraryAndDownloadAgainstHeadlessPak|RL_UITEST_PAIR_URL_LIBRARY
CLOSING|ErgonomicsUITests/testTheConsoleClosingAgainstHeadlessPak|RL_UITEST_PAIR_URL_CLOSING
EVENTS_QUIT|EventsUITests/testPakQuitReturnsToListAgainstHeadlessPak|RL_UITEST_PAIR_URL_EVENTS_QUIT
EVENTS_LIBRARY|EventsUITests/testLibraryRefreshFromAnotherClientAgainstHeadlessPak|RL_UITEST_PAIR_URL_EVENTS_LIBRARY
PAKS|PaksUITests/testInstallAndDeleteAgainstHeadlessPak|RL_UITEST_PAIR_URL_PAKS"

usage() { sed -n '2,42p' "$0" | sed 's/^# \{0,1\}//'; }
die() { echo "run-headless-uitests: $*" >&2; exit 1; }

if [ "${1-}" = "--list" ]; then
	printf '%s\n' "$TESTS" | awk -F'|' '{ printf "%-15s %s\n", $1, $2 }'
	exit 0
fi
case "${1-}" in -h | --help | -*) usage; exit 2 ;; esac

DESTINATION=${DESTINATION:-platform=iOS Simulator,name=iPhone 16}
DERIVED_DATA=${DERIVED_DATA:-build/uitests-dd}
PROJECT=ios/ReminisLink.xcodeproj

# --- the selection -------------------------------------------------------

selected=""
want() { case " $selected " in *" $1 "*) ;; *) selected="$selected $1" ;; esac; }
if [ $# -eq 0 ]; then
	selected=$(printf '%s\n' "$TESTS" | cut -d'|' -f1 | tr '\n' ' ')
else
	for arg in "$@"; do
		name=$(printf '%s\n' "$TESTS" | awk -F'|' -v a="$arg" '$1 == a || $2 == a || $2 ~ ("/" a "$") { print $1; exit }')
		[ -n "$name" ] || die "unknown test '$arg' (see --list)"
		[ "$name" != RESTORED ] || want STATES
		want "$name"
	done
fi
# Keep the order of $TESTS.
ordered=""
while IFS='|' read -r name _ _; do
	case " $selected " in *" $name "*) ordered="$ordered $name" ;; esac
done <<EOF
$TESTS
EOF

# --- work folder, processes ---------------------------------------------------

work=$(mktemp -d "${TMPDIR:-/tmp}/rl-uitests.XXXXXX")
pak_pid=""
helper_pid=""
stop_pak() {
	if [ -n "$pak_pid" ]; then
		kill -TERM "$pak_pid" 2>/dev/null || true
		wait "$pak_pid" 2>/dev/null || true
		pak_pid=""
	fi
}
stop_helper() {
	if [ -n "$helper_pid" ]; then
		kill "$helper_pid" 2>/dev/null || true
		wait "$helper_pid" 2>/dev/null || true
		helper_pid=""
	fi
}
cleanup() {
	stop_helper
	stop_pak
	if [ "${KEEP_LOGS:-0}" = 1 ]; then echo "work folder kept: $work"; else rm -rf "$work"; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

now() { python3 -c 'import time; print("%.3f" % time.time())' 2>/dev/null || date +%s; }

# start_pak CARD LOG: starts the headless pak on CARD with pairing open,
# its standard output to LOG.
start_pak() {
	: >"$2"
	build/reminislink-host --headless --sd "$1" --pairing-open --name "UI tests pak" >"$2" 2>&1 &
	pak_pid=$!
}

# links LOG: how many pairing links the pak printed.
links() { grep -c '^Pairing link: ' "$1" 2>/dev/null || true; }

# wait_links LOG N SECONDS: waits until the pak printed N links.
wait_links() {
	local i
	for ((i = 0; i < $3 * 10; i++)); do
		[ "$(links "$1")" -ge "$2" ] && return 0
		if [ -n "$pak_pid" ] && ! kill -0 "$pak_pid" 2>/dev/null; then return 1; fi
		sleep 0.1
	done
	return 1
}

# new_card: a fresh copy of testdata/sdcard; prints its path. Fails if the
# copy fails (set -e does not apply inside $(...) in bash 3.2: callers
# check the status).
new_card() {
	local card
	card=$(mktemp -d "$work/sdcard.XXXXXX") || return 1
	cp -R testdata/sdcard/. "$card/" || return 1
	printf '%s\n' "$card"
}

# --- build ---------------------------------------------------------------------

mkdir -p build
CGO_ENABLED=0 go build -o build/reminislink-host ./cmd/reminislink
echo "pak built: build/reminislink-host"
if [ "${SKIP_BUILD:-0}" != 1 ]; then
	echo "building the app and its UI tests (log: $work/build.log)"
	xcodebuild build-for-testing -project "$PROJECT" -scheme ReminisLink -destination "$DESTINATION" \
		-derivedDataPath "$DERIVED_DATA" >"$work/build.log" 2>&1 || {
		tail -n 30 "$work/build.log"
		KEEP_LOGS=1
		die "build-for-testing failed"
	}
fi

# --- the tests -------------------------------------------------------------------

passed=0
failed=0
skipped=0
states_card=""

for name in $ordered; do
	line=$(printf '%s\n' "$TESTS" | awk -F'|' -v n="$name" '$1 == n')
	test=$(printf '%s' "$line" | cut -d'|' -f2)
	variable=$(printf '%s' "$line" | cut -d'|' -f3)
	# The XCTest lines of this very method: a PASS needs its "passed" line,
	# so an empty run (e.g. a renamed method with SKIP_BUILD=1) fails.
	case_line="Test Case '-[ReminisLinkUITests.${test%/*} ${test#*/}]'"
	log=$work/$name.pak.log
	xlog=$work/$name.xcodebuild.log
	ready=$work/$name.ready
	trigger=$work/$name.trigger

	# A second attempt (fresh pak and link) only when the test never
	# started: no "started" line for the method, and the app never paired
	# (the pak printed only its first link, still unused).
	attempt=1
	while :; do
		rm -f "$ready" "$trigger"
		if [ "$name" = RESTORED ]; then
			[ -n "$states_card" ] || die "RESTORED needs the card of STATES"
			card=$states_card
			# Slot 1 of Starlight Courier was locked by STATES: change it by
			# hand while the pak is stopped; the next start restores it.
			echo changed >"$card/.userdata/shared/GBA-gpsp/Starlight Courier.gba.st1"
		else
			card=$(new_card) || die "cannot copy testdata/sdcard"
		fi
		[ "$name" != STATES ] || states_card=$card

		start_pak "$card" "$log"
		if ! wait_links "$log" 1 20; then
			stop_pak
			status=pak
			break
		fi

		# The person's part, in the background.
		case $name in
		CLOSING)
			( wait_links "$log" 2 120 && sleep 8 && kill -TERM "$pak_pid" ) >/dev/null 2>&1 &
			helper_pid=$!
			;;
		EVENTS_QUIT)
			(
				for ((i = 0; i < 1200; i++)); do [ -e "$ready" ] && break; sleep 0.1; done
				now >"$trigger"
				kill -TERM "$pak_pid"
			) >/dev/null 2>&1 &
			helper_pid=$!
			;;
		EVENTS_LIBRARY)
			# Once the app paired, the subshell becomes the second client
			# (exec): $helper_pid is then smoke-events.sh itself, whose
			# INT/TERM traps remove its private keys.
			(
				wait_links "$log" 2 120 || exit 1
				UI_DELETE=1 PAK_LOG=$log READY_FILE=$ready READY_TIMEOUT=120 TRIGGER_FILE=$trigger \
					CLIENT_NAME="UI tests B" exec scripts/smoke-events.sh
			) >"$work/$name.helper.log" 2>&1 &
			helper_pid=$!
			;;
		esac

		echo "running $test (pak PID $pak_pid)"
		status=0
		# The link goes to xcodebuild through its environment only (exported
		# in a subshell), never as an argument nor in a trace.
		(
			link=$(sed -n 's/^Pairing link: //p' "$log" | head -n 1)
			export "TEST_RUNNER_$variable=$link"
			export TEST_RUNNER_RL_UITEST_EVENTS_READY_FILE="$ready" TEST_RUNNER_RL_UITEST_EVENTS_TRIGGER_FILE="$trigger"
			exec xcodebuild test-without-building -project "$PROJECT" -scheme ReminisLink -destination "$DESTINATION" \
				-derivedDataPath "$DERIVED_DATA" -only-testing:"ReminisLinkUITests/$test"
		) >"$xlog" 2>&1 || status=$?

		# The second client is awaited only after a successful test; after a
		# failure it is stopped at once (it may wait for a pairing that will
		# never come).
		helper_status=0
		if [ "$status" -eq 0 ] && [ "$name" = EVENTS_LIBRARY ] && [ -n "$helper_pid" ]; then
			wait "$helper_pid" 2>/dev/null || helper_status=$?
			helper_pid=""
		fi
		stop_helper
		pak_links=$(links "$log")
		stop_pak

		if [ "$status" -ne 0 ] && [ "$attempt" -lt 2 ] && ! grep -qF "$case_line started" "$xlog" &&
			[ "${pak_links:-0}" -le 1 ]; then
			echo "retrying $name: the test did not start ($(grep -o 'encountered an error.*' "$xlog" | head -c 120 || true))"
			attempt=2
			continue
		fi
		break
	done

	if [ "$status" = pak ]; then
		echo "FAIL: $name (the pak printed no pairing link; output: $log)"
		KEEP_LOGS=1
		failed=$((failed + 1))
		continue
	fi
	delay=$(grep -o 'RL_EVENTS_DELAY [^"]*' "$xlog" | head -n 1 || true)
	if grep -qF "$case_line skipped" "$xlog"; then
		echo "SKIP: $name ($(grep -o 'Set TEST_RUNNER_[A-Z_]*' "$xlog" | head -n 1 || true); log: $xlog)"
		KEEP_LOGS=1
		skipped=$((skipped + 1))
	elif [ "$helper_status" -ne 0 ]; then
		echo "FAIL: $name (the second client failed; log: $work/$name.helper.log)"
		KEEP_LOGS=1
		failed=$((failed + 1))
	elif [ "$status" -eq 0 ] && grep -qF "$case_line passed" "$xlog"; then
		echo "PASS: $name${delay:+ ($delay)}"
		passed=$((passed + 1))
	else
		if grep -qF "$case_line started" "$xlog"; then
			echo "FAIL: $name (xcodebuild exit $status; log: $xlog)"
		else
			echo "FAIL: $name (the test did not run, xcodebuild exit $status; log: $xlog)"
		fi
		grep -E 'error: |XCTAssert|failed \(' "$xlog" | head -n 5 | sed 's/^/    /' || true
		KEEP_LOGS=1
		failed=$((failed + 1))
	fi
done

echo "run-headless-uitests: $passed passed, $failed failed, $skipped skipped"
[ "$failed" -eq 0 ] && [ "$skipped" -eq 0 ]
