#!/bin/bash
set -euo pipefail

test "$(snap debug confinement)" = strict

check_service() {
    local proxy_pid
    local status_code

    # Wait for the daemon to be ready, then inspect the Go process itself.
    for attempt in {1..30}; do
        if status_code=$(curl --silent --output /dev/null --write-out '%{http_code}' \
            --max-time 1 --noproxy '' --proxy http://127.0.0.1:8080 \
            http://example.com:22/); then
            test "$status_code" = 403
            break
        fi
        sleep 1
    done
    test "$status_code" = 403

    proxy_pid=$(systemctl show snap.esperanta.esperanta.service \
        --property MainPID --value)
    test "$proxy_pid" -gt 0

    sudo awk '
        /^Uid:/ { uid_ok = ($2 == 584792 && $3 == 584792 && $4 == 584792 && $5 == 584792) }
        /^Gid:/ { gid_ok = ($2 == 584792 && $3 == 584792 && $4 == 584792 && $5 == 584792) }
        /^Groups:/ { groups_ok = (NF == 1) }
        /^Cap(Inh|Prm|Eff|Bnd|Amb):/ { if ($2 !~ /^0+$/) bad_caps = 1; caps_seen++ }
        /^NoNewPrivs:/ { no_new_privs = ($2 == 1) }
        END { exit !(uid_ok && gid_ok && groups_ok && caps_seen == 5 && !bad_caps && no_new_privs) }
    ' "/proc/$proxy_pid/status"

    test "$(sudo cat "/proc/$proxy_pid/comm")" = esperanta
    sudo grep -q '^snap.esperanta.esperanta (enforce)$' "/proc/$proxy_pid/attr/current"
}

check_service
sudo snap run esperanta --check
sudo snap restart esperanta.esperanta
check_service
sudo snap stop esperanta.esperanta
test "$(systemctl show snap.esperanta.esperanta.service --property MainPID --value)" = 0
