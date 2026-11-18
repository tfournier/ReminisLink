#!/bin/sh
# ReminisLink - milestone M0: read-only survey of a NextUI device.
#
# Full procedure: docs/specs/m0/README.md. Run it twice on the handheld
# (SSH via the Dropbear pak, or the Terminal pak), before and after playing:
#   sh verify-device.sh > /mnt/SDCARD/survey-1-before.txt 2>&1
#   sh verify-device.sh > /mnt/SDCARD/survey-2-after.txt 2>&1
# Then copy both files into docs/specs/m0/ of the repository.
#
# It only reads: it creates, changes or deletes nothing (except its own output).

SD="${SDCARD_PATH:-/mnt/SDCARD}"

section() { printf '\n===== %s =====\n' "$1"; }
limit() { head -n "${1:-40}"; }

section "Date and kernel"
date -u 2>/dev/null
uname -a 2>/dev/null
cat /proc/uptime 2>/dev/null

section "Environment (NextUI variables)"
env | grep -E '^(PLATFORM|DEVICE|SDCARD_PATH|ROMS_PATH|SAVES_PATH|CHEATS_PATH|USERDATA_PATH|SHARED_USERDATA_PATH|LOGS_PATH|BIOS_PATH|HOME|PATH|LD_LIBRARY_PATH)=' 2>/dev/null

section "NextUI version files"
for f in "$SD"/.system/version.txt "$SD"/.system/*/version.txt "$SD"/version.txt "$SD"/.system/*.txt; do
	[ -f "$f" ] && { echo "--- $f"; limit 10 < "$f"; }
done
ls -la "$SD/.system" 2>/dev/null | limit 30

section "SD card root"
ls -la "$SD" 2>/dev/null
df -h "$SD" 2>/dev/null
mount 2>/dev/null | grep -i -E 'sdcard|mmc' | limit 10

section "Roms folders (systems)"
ls -1 "$SD/Roms" 2>/dev/null | limit 80

section "Sample of one system with .media"
for d in "$SD"/Roms/*; do
	[ -d "$d/.media" ] || continue
	echo "--- $d"
	ls -1 "$d" | limit 10
	echo "--- $d/.media"
	ls -1 "$d/.media" | limit 10
	break
done

section "m3u files (multi-disc)"
find "$SD/Roms" -maxdepth 3 -iname '*.m3u' 2>/dev/null | limit 10

section "Saves layout"
ls -1 "$SD/Saves" 2>/dev/null | limit 60
find "$SD/Saves" -maxdepth 2 -type f 2>/dev/null | limit 20

section "Cheats layout"
find "$SD/Cheats" -maxdepth 2 -type f 2>/dev/null | limit 10

section "Userdata layout"
ls -la "$SD/.userdata" 2>/dev/null
ls -la "$SD/.userdata/shared" 2>/dev/null | limit 40
ls -la "$SD/.userdata/shared/.minui" 2>/dev/null | limit 60

section "Save states: any file named *.st0-*.st9 or *.state*"
find "$SD" -path "$SD/Roms" -prune -o -type f \( -name '*.st[0-9]' -o -name '*.state*' \) -print 2>/dev/null | limit 40

section "Save state thumbnails: images next to states"
find "$SD/.userdata" -type f \( -name '*.st[0-9].png' -o -name '*.st[0-9].bmp' -o -name '*.png' \) -path '*minui*' 2>/dev/null | limit 20

section "Slot 9 (resume?) files"
find "$SD/.userdata" -type f -name '*.st9' 2>/dev/null | limit 10

section "Recently modified files under .userdata (last 3 days)"
find "$SD/.userdata" -type f -mtime -3 2>/dev/null | limit 40

section "Game tracker / databases"
find "$SD/.userdata" -maxdepth 3 -type f \( -name '*.db' -o -name '*.sqlite*' \) 2>/dev/null | limit 20

section "Battery"
for p in /sys/class/power_supply/*; do
	[ -d "$p" ] || continue
	echo "--- $p"
	for k in type status capacity online present voltage_now; do
		[ -f "$p/$k" ] && printf '%s=%s\n' "$k" "$(cat "$p/$k" 2>/dev/null)"
	done
done

section "Network"
ip -4 addr 2>/dev/null || ifconfig 2>/dev/null
ip route 2>/dev/null
cat /proc/net/wireless 2>/dev/null
(command -v iw >/dev/null && iw dev) 2>/dev/null
(command -v wpa_cli >/dev/null && wpa_cli status 2>/dev/null | grep -E '^(ssid|wpa_state|ip_address)=') 2>/dev/null

section "mDNS / avahi"
command -v avahi-daemon; ls /etc/avahi 2>/dev/null; ps 2>/dev/null | grep -i -E 'avahi|mdns' | grep -v grep
netstat -lnu 2>/dev/null | grep 5353

section "Listening ports"
netstat -lnt 2>/dev/null | limit 30

section "CA certificates (for HTTPS downloads)"
ls /etc/ssl/certs 2>/dev/null | limit 5
ls -la /etc/ssl/certs/ca-certificates.crt 2>/dev/null

section "CPU and crypto"
grep -E 'model name|Features|Hardware|CPU part' /proc/cpuinfo 2>/dev/null | sort -u | limit 10

section "Tools available"
for t in iperf3 curl wget sqlite3 sha256sum dropbear busybox; do
	printf '%s: ' "$t"; command -v "$t" || echo "missing"
done

section "Installed paks"
ls -1 "$SD/Tools/${PLATFORM:-tg5040}" 2>/dev/null

section "Done"
echo "Next: follow step 4 of docs/specs/m0/README.md (in-game save, states in"
echo "slots 1 and 2, power off in game), then run this script again."
