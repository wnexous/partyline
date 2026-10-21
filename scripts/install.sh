#!/bin/sh
# Partyline installer for Linux and macOS (amd64, arm64).
#
#   curl -fsSL https://<your instance>/install.sh | sh
#
# Downloads the latest release binary from GitHub, verifies its SHA-256 against the release's
# checksums.txt and installs it to ~/.local/bin (or $PARTYLINE_INSTALL_DIR). When served by a
# Partyline instance, the instance URL becomes the default for `partyline setup`.
#
# Environment:
#   PARTYLINE_VERSION      release tag to install (default: the latest release)
#   PARTYLINE_INSTALL_DIR  install directory (default: ~/.local/bin)
#   PARTYLINE_URL          instance URL saved as the setup default (default: the serving instance)
#
# Everything runs from main, called on the last line: a download cut short defines functions
# at most and runs nothing (ONB-10). Plain http URLs are refused except for loopback hosts.
set -eu

DEFAULT_URL='__PARTYLINE_URL__'
REPO=wnexous/partyline

fail() {
	echo "partyline install: $*" >&2
	exit 1
}

fetch() { # fetch <url> <file>
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2" || fail "download failed: $1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1" || fail "download failed: $1"
	else
		fail "curl or wget is required"
	fi
}

sha256() { # sha256 <file>
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		fail "sha256sum or shasum is required"
	fi
}

require_https() { # require_https <url>: plain http only for localhost, 127.0.0.1 and ::1
	case "$1" in
	http://localhost | http://localhost[:/]* | http://127.0.0.1 | http://127.0.0.1[:/]* | "http://[::1]" | "http://[::1]"[:/]*) ;;
	http://* | HTTP://*) fail "refusing plain http URL $1: use https" ;;
	esac
}

main() {
	RELEASES_API="${PARTYLINE_RELEASES_API:-https://api.github.com/repos/$REPO/releases/latest}"
	RELEASE_BASE="${PARTYLINE_RELEASE_BASE:-https://github.com/$REPO/releases/download}"
	url="${PARTYLINE_URL:-}"
	case "$DEFAULT_URL" in
	__*) ;;
	*) [ -n "$url" ] || url="$DEFAULT_URL" ;;
	esac
	require_https "$RELEASES_API"
	require_https "$RELEASE_BASE"
	[ -z "$url" ] || require_https "$url"

	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported system $(uname -s): download a release from https://github.com/$REPO/releases" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "unsupported architecture $(uname -m): download a release from https://github.com/$REPO/releases" ;;
	esac

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t partyline)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	tag="${PARTYLINE_VERSION:-}"
	if [ -z "$tag" ]; then
		fetch "$RELEASES_API" "$tmp/latest.json"
		tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
		[ -n "$tag" ] || fail "could not find the latest release"
	fi
	case "$tag" in
	v*) ;;
	*) tag="v$tag" ;;
	esac
	version="${tag#v}"

	asset="partyline_${version}_${os}_${arch}.tar.gz"
	echo "Downloading partyline $tag ($os/$arch)..."
	fetch "$RELEASE_BASE/$tag/$asset" "$tmp/$asset"
	fetch "$RELEASE_BASE/$tag/checksums.txt" "$tmp/checksums.txt"

	want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")
	[ -n "$want" ] || fail "$asset is not listed in checksums.txt"
	got=$(sha256 "$tmp/$asset")
	[ "$want" = "$got" ] || fail "checksum mismatch for $asset: nothing was installed"

	mkdir "$tmp/x"
	tar -xzf "$tmp/$asset" -C "$tmp/x" || fail "could not unpack $asset"
	[ -f "$tmp/x/partyline" ] || fail "$asset has no partyline binary"

	dir="${PARTYLINE_INSTALL_DIR:-$HOME/.local/bin}"
	mkdir -p "$dir" || fail "cannot create $dir"
	cp "$tmp/x/partyline" "$dir/.partyline.new" || fail "cannot write to $dir"
	chmod 755 "$dir/.partyline.new"
	mv -f "$dir/.partyline.new" "$dir/partyline" || fail "cannot write to $dir"
	echo "Installed partyline $tag to $dir/partyline"

	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		echo
		echo "$dir is not on your PATH. Add it, e.g. to ~/.profile or your shell's rc file:"
		echo "  export PATH=\"$dir:\$PATH\""
		;;
	esac

	# Save the instance URL as the default for `partyline setup` unless a config file exists.
	if [ -n "$url" ]; then
		config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/partyline"
		if [ ! -e "$config_dir/config.json" ]; then
			escaped=$(printf '%s' "$url" | sed 's/\\/\\\\/g; s/"/\\"/g')
			(
				umask 077
				mkdir -p "$config_dir" &&
					printf '{\n  "url": "%s",\n  "token": ""\n}\n' "$escaped" >"$config_dir/config.json"
			) || fail "cannot write $config_dir/config.json"
		fi
		echo
		echo "Next: partyline setup --browser"
	else
		echo
		echo "Next: partyline setup --browser --url https://<your instance>"
	fi
}

main "$@"
