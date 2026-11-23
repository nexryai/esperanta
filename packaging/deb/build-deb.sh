#!/usr/bin/env bash
# Build a Debian package from an already built esperanta binary.
#
# Usage: build-deb.sh <debian-arch> <binary> <version> <output-dir>
# Example: build-deb.sh amd64 esperanta-amd64 0.1.2 dist
set -euo pipefail

if [ "$#" -ne 4 ]; then
    echo "usage: $0 <debian-arch> <binary> <version> <output-dir>" >&2
    exit 2
fi

ARCH="$1"
BINARY="$2"
VERSION="$3"
OUTDIR="$4"

ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT

install -d "$ROOT/DEBIAN" \
    "$ROOT/usr/bin" \
    "$ROOT/usr/lib/systemd/system" \
    "$ROOT/etc/esperanta" \
    "$ROOT/usr/share/doc/esperanta"

install -m 0755 "$BINARY" "$ROOT/usr/bin/esperanta"
install -m 0644 systemd/esperanta.service "$ROOT/usr/lib/systemd/system/esperanta.service"
install -m 0644 config.yaml "$ROOT/etc/esperanta/config.yaml"
install -m 0644 LICENSE "$ROOT/usr/share/doc/esperanta/copyright"
install -m 0644 README.md "$ROOT/usr/share/doc/esperanta/README.md"

cat > "$ROOT/DEBIAN/control" <<EOF
Package: esperanta
Version: ${VERSION}-1
Architecture: ${ARCH}
Maintainer: nexryai <nexryai@users.noreply.github.com>
Section: net
Priority: optional
Depends: systemd, ca-certificates
Homepage: https://github.com/nexryai/esperanta
Description: IP-restricted HTTP and HTTPS forward proxy
 Esperanta is a simple forward proxy with YAML configuration, connection
 pooling, bounded concurrency and HTTPS CONNECT support. The daemon runs as a
 DynamicUser and applies a read-only Landlock sandbox.
EOF

echo /etc/esperanta/config.yaml > "$ROOT/DEBIAN/conffiles"

cat > "$ROOT/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
EOF

cat > "$ROOT/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] && command -v systemctl >/dev/null 2>&1; then
    systemctl stop esperanta.service >/dev/null 2>&1 || true
    systemctl disable esperanta.service >/dev/null 2>&1 || true
fi
exit 0
EOF

cat > "$ROOT/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
EOF

chmod 0755 "$ROOT/DEBIAN/postinst" "$ROOT/DEBIAN/prerm" "$ROOT/DEBIAN/postrm"

mkdir -p "$OUTDIR"
dpkg-deb --root-owner-group --build "$ROOT" \
    "${OUTDIR}/esperanta_${VERSION}-1_${ARCH}.deb"
