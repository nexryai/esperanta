#!/usr/bin/env bash
# Build the esperanta Arch Linux package from the upstream release tarball.
#
# This script must run inside an Arch Linux environment (or the
# archlinux:latest container) with base-devel, go and curl installed. The
# release workflow calls it in a throwaway container so the host needs no Arch
# tooling. aarch64 is cross-compiled on x86_64.
#
# Usage: packaging/arch/build.sh <version> <arch> [output-dir]
#   version     release tag without the leading "v" (e.g. 0.1.2)
#   arch        x86_64 or aarch64
#   output-dir  directory for the .pkg.tar.zst (default: dist)
set -euo pipefail

usage() {
	echo "usage: $0 <version> <arch> [output-dir]" >&2
	exit 2
}

[[ $# -ge 2 && $# -le 3 ]] || usage

tag="${1#v}"
arch="$2"
outdir="${3:-dist}"

case "$tag" in
	[0-9]*) ;;
	*) echo "invalid version: $tag" >&2; exit 2 ;;
esac
case "$arch" in
	x86_64 | aarch64) ;;
	*) echo "unsupported arch: $arch" >&2; exit 2 ;;
esac

# makepkg rejects hyphens in pkgver, but release tags may contain them.
pkgver="${tag//-/_}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pkgname=esperanta

for tool in makepkg sha256sum curl; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "missing required tool: $tool" >&2
		exit 1
	}
done

mkdir -p "$outdir"
outdir="$(cd "$outdir" && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

repo_url="https://github.com/nexryai/esperanta"
src_url="$repo_url/archive/refs/tags/v${tag}.tar.gz"
tarball="$work/${pkgname}-${pkgver}.tar.gz"
echo "Downloading $src_url"
curl -fsSL --retry 3 "$src_url" -o "$tarball"
sha="$(sha256sum "$tarball" | cut -d' ' -f1)"

# Pin a copy of the PKGBUILD to the exact tag being released. The checked-in
# PKGBUILD is kept in sync by the release workflow.
sed -E \
	-e "s/^pkgver=.*/pkgver=${pkgver}/" \
	-e "s/^pkgrel=.*/pkgrel=1/" \
	-e "s/^sha256sums=.*/sha256sums=('${sha}')/" \
	-e "s|^source=.*|source=(\"$pkgname-$pkgver.tar.gz::$src_url\")|" \
	"$script_dir/PKGBUILD" >"$work/PKGBUILD"

cross=false
if [[ "$arch" != "$(uname -m)" ]]; then
	cross=true
fi

{
	echo 'source /etc/makepkg.conf'
	printf 'PKGDEST=%q\n' "$outdir"
	printf 'SRCDEST=%q\n' "$work"
	printf 'BUILDDIR=%q\n' "$work/build"
	echo "PKGEXT='.pkg.tar.zst'"
	if $cross; then
		printf 'CARCH=%q\n' "$arch"
		printf 'CHOST=%q\n' "$arch-unknown-linux-gnu"
	fi
} >"$work/makepkg.conf"

makepkg_args=(--config "$work/makepkg.conf" --noconfirm)
if $cross; then
	makepkg_args+=(--nocheck)
fi

cd "$work"
if [[ $EUID -eq 0 ]]; then
	if ! id -u builder >/dev/null 2>&1; then
		useradd -m builder
	fi
	chown -R builder "$work" "$outdir"
	runuser -u builder -- makepkg "${makepkg_args[@]}"
else
	makepkg "${makepkg_args[@]}"
fi

shopt -s nullglob
packages=("$outdir"/"${pkgname}-${pkgver}"-*.pkg.tar.zst)
shopt -u nullglob
if [[ ${#packages[@]} -eq 0 ]]; then
	echo "makepkg produced no package in $outdir" >&2
	exit 1
fi

printf 'Built:\n'
printf '  %s\n' "${packages[@]}"
