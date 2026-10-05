#!/bin/sh
set -eu

repo="Aaravkhanal/GITOWN"
base="https://github.com/$repo"
command -v curl >/dev/null 2>&1 || { echo "gitown installer requires curl" >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { echo "gitown installer requires tar" >&2; exit 1; }
command -v cosign >/dev/null 2>&1 || { echo "Install Sigstore cosign first: https://docs.sigstore.dev/cosign/system_config/installation/" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in linux|darwin) ;; *) echo "unsupported operating system: $os" >&2; exit 1 ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported architecture: $arch" >&2; exit 1 ;; esac

release=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest")
version=$(printf '%s' "$release" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
[ -n "$version" ] || { echo "could not determine latest GITOWN release" >&2; exit 1; }
asset="gitown_${version#v}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
for file in "$asset" checksums.txt checksums.bundle; do
  curl -fsSL "$base/releases/download/$version/$file" -o "$tmp/$file"
done
cosign verify-blob --bundle "$tmp/checksums.bundle" \
  --certificate-identity-regexp "^https://github.com/$repo/.github/workflows/release.yml@refs/tags/.+$" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$tmp/checksums.txt"
(cd "$tmp" && grep "  $asset\$" checksums.txt | shasum -a 256 -c -)
tar -xzf "$tmp/$asset" -C "$tmp"
install_dir=${GITOWN_INSTALL_DIR:-"$HOME/.local/bin"}
mkdir -p "$install_dir"
install "$tmp/gitown_${version#v}_${os}_${arch}/gitown" "$install_dir/gitown"
printf 'Installed gitown %s to %s/gitown\n' "$version" "$install_dir"
case ":$PATH:" in *":$install_dir:"*) ;; *) echo "Add $install_dir to PATH to use gitown." ;; esac
