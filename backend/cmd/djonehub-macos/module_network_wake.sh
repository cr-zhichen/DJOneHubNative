#!/bin/sh

# Keep the QDC507 data path awake while an iPhone/iPad is attached. No mobile
# companion app or private Agent is required.
set -u

NAME=djonehub_network_wake
ENABLED=/data/djonehub/network-wake.enabled
PIDFILE=/var/run/djonehub_network_wake.pid
USB_STATE=/sys/class/android_usb/android0/state
USB_FUNCTIONS=/sys/class/android_usb/android0/functions
WAKE_LOCK=/sys/power/wake_lock
WAKE_UNLOCK=/sys/power/wake_unlock
locked=0

release_lock() {
    if test "$locked" = 1 && test -w "$WAKE_UNLOCK"; then
        echo "$NAME" >"$WAKE_UNLOCK" 2>/dev/null || true
    fi
    locked=0
}

cleanup() {
    release_lock
    rm -f "$PIDFILE"
}

mobile_usb_active() {
    test -f "$ENABLED" || return 1
    test "$(cat "$USB_STATE" 2>/dev/null)" = CONFIGURED || return 1
    functions=$(cat "$USB_FUNCTIONS" 2>/dev/null || true)
    case ",$functions," in *,ecm,*) ;; *) return 1;; esac
    # v1.2.9 mobile mode removes USB Audio but may retain serial/ADB.
    case ",$functions," in *,audio,*) return 1;; esac
    return 0
}

acquire_lock() {
    test "$locked" = 1 && return 0
    if test -w "$WAKE_LOCK"; then
        echo "$NAME" >"$WAKE_LOCK" 2>/dev/null && locked=1
    fi
}

host_address() {
    for dev in ecm0 bridge0 usb0 rndis0; do
        test -d "/sys/class/net/$dev" || continue
        candidate=$(ip neigh show dev "$dev" 2>/dev/null | awk '$1 ~ /^192\.168\.225\./ && $1 != "192.168.225.1" { print $1; exit }')
        test -n "$candidate" && { echo "$candidate"; return; }
    done
    echo 192.168.225.2
}

trap cleanup EXIT INT TERM HUP
echo $$ >"$PIDFILE"

while :; do
    if mobile_usb_active; then
        acquire_lock
        host=$(host_address)
        busybox ping -c 1 -W 2 "$host" >/dev/null 2>&1 || true
        busybox ping -c 1 -W 2 223.5.5.5 >/dev/null 2>&1 || true
        sleep 15
    else
        release_lock
        sleep 3
    fi
done
