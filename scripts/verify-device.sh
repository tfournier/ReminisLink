#!/bin/sh
# ReminisLink - milestone M0: read-only survey of a NextUI device.
#
# Reduced procedure: docs/specs/m0/README.md. The NextUI paths are already
# known from the source analysis (docs/specs/m0/source-analysis.md); this
# script confirms them and records what only the device can tell (sleep,
# avahi, RTC/NTP, battery files, version file format).
# Run it twice on the handheld (SSH via the Dropbear pak, or the Terminal pak):
#   sh verify-device.sh > /mnt/SDCARD/survey-1-before.txt 2>&1
#   sh verify-device.sh > /mnt/SDCARD/survey-2-after.txt 2>&1
# Then copy both files into docs/specs/m0/ of the repository.
#
# It only reads: it creates, changes or deletes nothing (except its own output).

SD="${SDCARD_PATH:-/mnt/SDCARD}"
PLAT="${PLATFORM:-tg5040}"
SHARED="${SHARED_USERDATA_PATH:-$SD/.userdata/shared}"
PS_DIR=/sys/class/power_supply

section() { printf '\n===== %s =====\n' "$1"; }
limit() { head -n "${1:-40}"; }
procs() { ps 2>/dev/null | grep -E "$1" | grep -v grep; }

section "Date, clock and kernel"
echo "date (local): $(date 2>/dev/null)"
echo "date (UTC):   $(date -u 2>/dev/null)"
ls -l /dev/rtc* 2>/dev/null
(command -v hwclock >/dev/null && echo "hwclock -r -u: $(hwclock -r -u 2>&1)") 2>/dev/null
uname -a 2>/dev/null
cat /proc/uptime 2>/dev/null

section "NTP"
procs 'ntpd' || echo "no ntpd process"
(command -v uci >/dev/null && {
	echo "uci system.ntp.enable=$(uci -q get system.ntp.enable)"
	echo "uci system.ntp.enabled=$(uci -q get system.ntp.enabled)"
	echo "uci zonename=$(uci -q get system.@system[0].zonename)"
}) 2>/dev/null

section "Environment (NextUI variables)"
env | grep -E '^(PLATFORM|DEVICE|TRIMUI_MODEL|IS_NEXT|SDCARD_PATH|ROMS_PATH|SAVES_PATH|CHEATS_PATH|USERDATA_PATH|SHARED_USERDATA_PATH|LOGS_PATH|BIOS_PATH|SYSTEM_PATH|HOME|PATH|LD_LIBRARY_PATH)=' 2>/dev/null

section "NextUI version file (.system/version.txt, raw bytes)"
if [ -f "$SD/.system/version.txt" ]; then
	limit 5 < "$SD/.system/version.txt"
	echo "--- od -c"
	od -c "$SD/.system/version.txt" 2>/dev/null | limit 8
else
	echo "missing: $SD/.system/version.txt"
fi

section "Stock firmware version"
cat /etc/version 2>/dev/null
grep -E '^DISTRIB_(ID|RELEASE|REVISION)=' /etc/openwrt_release 2>/dev/null

section "SD card"
df -h "$SD" 2>/dev/null
mount 2>/dev/null | grep -i -E 'sdcard|mmc' | limit 10

section "NextUI settings (minuisettings.txt)"
if [ -f "$SHARED/minuisettings.txt" ]; then
	grep -E '^(saveFormat|stateFormat|useExtractedFileName)=' "$SHARED/minuisettings.txt" \
		|| echo "(saveFormat/stateFormat/useExtractedFileName not set: defaults 0)"
else
	echo "missing: $SHARED/minuisettings.txt (defaults apply)"
fi

section "Roms folders (systems) and emulator paks"
ls -1 "$SD/Roms" 2>/dev/null | limit 80
echo "--- .system/$PLAT/paks/Emus"
ls -1 "$SD/.system/$PLAT/paks/Emus" 2>/dev/null | limit 60
echo "--- Emus/$PLAT"
ls -1 "$SD/Emus/$PLAT" 2>/dev/null | limit 60

section "Saves layout"
ls -1 "$SD/Saves" 2>/dev/null | limit 60
find "$SD/Saves" -maxdepth 2 -type f 2>/dev/null | limit 20

section "State folders (.userdata/shared/<TAG>-<core>/)"
for d in "$SHARED"/*-*; do
	[ -d "$d" ] || continue
	echo "--- $d"
	ls -la "$d" 2>/dev/null | limit 15
done

section "State files anywhere outside Roms (*.st0-*.st9, *.state*)"
find "$SD" -path "$SD/Roms" -prune -o -type f \( -name '*.st[0-9]' -o -name '*.state*' \) -print 2>/dev/null | limit 40

section "Resume slot files (*.st9) and auto_resume.txt"
find "$SHARED" -type f -name '*.st9' 2>/dev/null | limit 10
ls -la "$SHARED/.minui/auto_resume.txt" 2>/dev/null || echo "no auto_resume.txt"

section "State thumbnails and slot files (.minui/<TAG>/*.<N>.bmp, *.txt)"
ls -la "$SHARED/.minui" 2>/dev/null | limit 30
find "$SHARED/.minui" -mindepth 2 -type f \( -name '*.[0-9].bmp' -o -name '*.txt' \) 2>/dev/null | limit 30
first_bmp=$(find "$SHARED/.minui" -mindepth 2 -type f -name '*.[0-9].bmp' 2>/dev/null | head -n 1)
if [ -n "$first_bmp" ]; then
	echo "--- first 24 bytes of $first_bmp (PNG signature, then IHDR width/height at bytes 16-23)"
	head -c 24 "$first_bmp" | od -A d -t x1 2>/dev/null
	ls -l "$first_bmp"
fi

section "Recently modified files under .userdata (last 3 days)"
find "$SD/.userdata" -type f -mtime -3 2>/dev/null | limit 40

section "Battery (axp2202-*)"
for p in "$PS_DIR"/axp2202-*; do
	[ -d "$p" ] || continue
	echo "--- $p"
	ls "$p" 2>/dev/null | tr '\n' ' '; echo
	for k in type status capacity online present time_to_full_now time_to_empty_now voltage_now current_now; do
		[ -f "$p/$k" ] && printf '%s=%s\n' "$k" "$(cat "$p/$k" 2>/dev/null)"
	done
done
ls "$PS_DIR" 2>/dev/null

section "Network"
ip -4 addr show wlan0 2>/dev/null || ifconfig wlan0 2>/dev/null
ip route 2>/dev/null
cat /proc/net/wireless 2>/dev/null
(command -v iw >/dev/null && iw dev wlan0 get power_save) 2>/dev/null
(command -v wpa_cli >/dev/null && wpa_cli -p /etc/wifi/sockets -i wlan0 status 2>/dev/null | grep -E '^(ssid|wpa_state|ip_address)=') 2>/dev/null
procs 'wpa_supplicant|udhcpc'

section "mDNS / avahi (UDP 5353)"
procs 'avahi|dbus' || echo "no avahi or dbus process"
ls /etc/rc.d 2>/dev/null | grep -i -E 'avahi|dbus'
grep -E '^(host-name|enable-dbus|publish-addresses|use-ipv6)=' /etc/avahi/avahi-daemon.conf 2>/dev/null
netstat -lnup 2>/dev/null | grep 5353 || netstat -lnu 2>/dev/null | grep 5353

section "Listening TCP ports"
netstat -lnt 2>/dev/null | limit 30

section "Sleep and power daemons"
procs 'keymon|batmon|audiomon|trimui_inputd'
cat /sys/power/state 2>/dev/null

section "Tools available"
for t in iperf3 curl wget wpa_cli iw hwclock uci sha256sum dropbear busybox; do
	printf '%s: ' "$t"; command -v "$t" || echo "missing"
done

section "Installed tool paks"
ls -1 "$SD/Tools/$PLAT" 2>/dev/null

section "Done"
echo "See docs/specs/m0/README.md for the next step."
