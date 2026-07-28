#!/bin/sh
# succubus installer for macOS, Linux, and FreeBSD.
#
#   curl -fsSL https://raw.githubusercontent.com/enowdev/succubus/main/install.sh | sh
#
# Downloads the release binary for this platform, verifies its checksum, and
# puts it somewhere on your PATH. Nothing here needs root: the default target is
# ~/.local/bin, and /usr/local/bin is only used if it is already writable.
#
# POSIX sh on purpose — this has to run under dash, ash (Alpine), and the
# ancient bash that ships with macOS.
set -eu

REPO="${SUCCUBUS_REPO:-enowdev/succubus}"
BIN="succubus"

# Where releases are fetched from. Overridable so a fork can point elsewhere —
# and so this script can be tested end to end against a local server instead of
# only being read and hoped about.
RELEASE_BASE="${SUCCUBUS_RELEASE_BASE:-https://github.com/$REPO/releases/download}"
API_BASE="${SUCCUBUS_API_BASE:-https://api.github.com}"

# --- output ------------------------------------------------------------------
# Colour only when stdout is a terminal and NO_COLOR is unset.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  DIM=$(printf '\033[2m'); RED=$(printf '\033[31m')
  GREEN=$(printf '\033[32m'); YELLOW=$(printf '\033[33m'); OFF=$(printf '\033[0m')
else
  DIM=''; RED=''; GREEN=''; YELLOW=''; OFF=''
fi

# ASCII markers on purpose: this runs in whatever terminal and locale the user
# has, and a mojibake checkmark next to "installed" reads as a failure.
say()  { printf '  %s\n' "$*"; }
dim()  { printf '  %s%s%s\n' "$DIM" "$*" "$OFF"; }
ok()   { printf '  %s[ok]%s %s\n' "$GREEN" "$OFF" "$*"; }
warn() { printf '  %s[!]%s %s\n' "$YELLOW" "$OFF" "$*"; }
# REPORTED marks a failure the script already explained, so the exit trap does
# not print a second, vaguer message on top of it.
REPORTED=0
die()  { REPORTED=1; printf '\n  %s[x]%s %s\n\n' "$RED" "$OFF" "$*" >&2; exit 1; }

TMP=''

# An installer that exits non-zero without saying anything is a support ticket;
# one that exits 0 after failing is worse, because the user believes it worked.
# This catches both: every abnormal exit is reported, and the staging directory
# is removed on any path out.
cleanup() {
  st=$?   # captured first: everything below must not disturb it
  if [ -n "$TMP" ]; then rm -rf "$TMP"; fi
  if [ "$st" -ne 0 ] && [ "$REPORTED" -eq 0 ]; then
    printf '\n  %s[x]%s install failed (exit %s). Nothing was installed.\n\n' \
      "$RED" "$OFF" "$st" >&2
  fi
  exit $st
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# --- platform ----------------------------------------------------------------
detect_platform() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    darwin|linux|freebsd) ;;
    *) die "unsupported operating system: $os

For Windows, use the PowerShell installer instead:
  irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex" ;;
  esac

  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture: $arch (succubus ships amd64 and arm64)" ;;
  esac

  PLATFORM="${os}-${arch}"
}

# --- download tooling --------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }

fetch() {  # fetch <url> <dest>
  if have curl; then
    curl -fsSL --retry 3 --retry-delay 1 -o "$2" "$1"
  elif have wget; then
    wget -qO "$2" "$1"
  else
    die "neither curl nor wget is available"
  fi
}

fetch_stdout() {
  if have curl; then curl -fsSL --retry 3 "$1"
  elif have wget; then wget -qO- "$1"
  else die "neither curl nor wget is available"; fi
}

# --- version -----------------------------------------------------------------
resolve_version() {
  if [ -n "${SUCCUBUS_VERSION:-}" ]; then
    VERSION="$SUCCUBUS_VERSION"
    return
  fi
  # Ask the API rather than parsing the redirect: it works without a browser
  # and gives a clear error when no release exists yet.
  tag=$(fetch_stdout "$API_BASE/repos/$REPO/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$tag" ] || die "could not determine the latest release.

Set a version explicitly, or build from source:
  SUCCUBUS_VERSION=v0.1.0 sh install.sh
  git clone https://github.com/$REPO && cd succubus && make install"
  VERSION="$tag"
}

# --- checksum ----------------------------------------------------------------
# Verifying matters here: this script pipes a downloaded binary straight onto
# your PATH. A silent mismatch is exactly what you do not want.
#
# This fails closed. If the checksum cannot be checked — none published, no
# sha256 tool on the machine — the install stops rather than quietly proceeding
# unverified, because "verification silently did not happen" looks identical to
# "verification passed" in the output. Setting SUCCUBUS_SKIP_VERIFY=1 opts out,
# which makes the decision the user's and leaves it visible in their shell
# history.
cannot_verify() {  # cannot_verify <reason>
  if [ "${SUCCUBUS_SKIP_VERIFY:-0}" = "1" ]; then
    warn "$1 — continuing because SUCCUBUS_SKIP_VERIFY=1"
    return 0
  fi
  die "$1, so the download cannot be verified.

Refusing to install an unverified binary. Either use a release that publishes
checksums.txt, install a sha256 tool, or accept the risk explicitly:

  SUCCUBUS_SKIP_VERIFY=1 sh install.sh"
}

verify_checksum() {  # verify_checksum <file> <sums-file> <name>
  want=$(grep " $3\$" "$2" 2>/dev/null | awk '{print $1}' | head -1)
  [ -n "$want" ] || { cannot_verify "no checksum is published for $3"; return 0; }

  if have sha256sum;   then got=$(sha256sum "$1" | awk '{print $1}')
  elif have shasum;    then got=$(shasum -a 256 "$1" | awk '{print $1}')
  elif have sha256;    then got=$(sha256 -q "$1")
  else cannot_verify "no sha256 tool is available on this machine"; return 0
  fi

  [ "$got" = "$want" ] || die "checksum mismatch for $3
  expected $want
  got      $got
This is worth investigating rather than retrying."
  ok "checksum verified"
}

# --- install location --------------------------------------------------------
choose_dest() {
  if [ -n "${SUCCUBUS_INSTALL_DIR:-}" ]; then
    DEST="$SUCCUBUS_INSTALL_DIR"
  elif [ -w /usr/local/bin ] 2>/dev/null; then
    # Already writable without sudo, and almost always on PATH.
    DEST=/usr/local/bin
  else
    DEST="$HOME/.local/bin"
  fi
  mkdir -p "$DEST" || die "cannot create $DEST"
}

on_path() {
  case ":$PATH:" in *":$1:"*) return 0 ;; *) return 1 ;; esac
}

# --- main --------------------------------------------------------------------
printf '\n  succubus installer\n'
printf '  %s\n\n' "------------------------------------------------------------"

detect_platform
resolve_version
choose_dest

dim "platform  $PLATFORM"
dim "version   $VERSION"
dim "target    $DEST/$BIN"
printf '\n'

TMP=$(mktemp -d 2>/dev/null || mktemp -d -t succubus)   # removed by cleanup()

ASSET="succubus-${VERSION}-${PLATFORM}"
BASE="$RELEASE_BASE/$VERSION"

say "downloading ${ASSET}..."
# Silence curl's own error line: the message below says the same thing with the
# context to act on, and two errors for one failure just reads as noise.
fetch "$BASE/$ASSET" "$TMP/$BIN" 2>/dev/null || die "download failed: $BASE/$ASSET

If this platform has no published binary yet, build from source:
  git clone https://github.com/$REPO && cd succubus && make install"

if fetch "$BASE/checksums.txt" "$TMP/checksums.txt" 2>/dev/null; then
  verify_checksum "$TMP/$BIN" "$TMP/checksums.txt" "$ASSET"
else
  cannot_verify "this release publishes no checksums.txt"
fi

chmod +x "$TMP/$BIN"

# Replace atomically where possible: a running daemon keeps its inode, so this
# does not break an in-flight process the way overwriting in place would.
if ! mv "$TMP/$BIN" "$DEST/$BIN" 2>/dev/null; then
  die "cannot write to $DEST

Try a different location:
  SUCCUBUS_INSTALL_DIR=\$HOME/bin sh install.sh"
fi

ok "installed to $DEST/$BIN"

# Confirm it actually runs — a wrong-architecture binary fails here, not later.
if ! "$DEST/$BIN" version >/dev/null 2>&1; then
  die "the installed binary did not run. Wrong architecture for this machine?"
fi
ok "$("$DEST/$BIN" version)"

printf '\n'
if ! on_path "$DEST"; then
  warn "$DEST is not on your PATH, so \`$BIN\` will not be found."
  printf '\n'
  # These strings are printed for the user to copy, not evaluated here, so the
  # literal tilde and literal $PATH are intentional.
  # shellcheck disable=SC2088,SC2016
  case "${SHELL:-}" in
    */fish)
      # fish has no `export`, and its config lives elsewhere. Handing a fish
      # user a bash line means it silently does nothing.
      say "Add this to ~/.config/fish/config.fish, then restart your shell:"
      printf '\n      fish_add_path %s\n\n' "$DEST"
      ;;
    *)
      case "${SHELL:-}" in
        */zsh)  rc="~/.zshrc" ;;
        */bash) rc="~/.bashrc" ;;
        *)      rc="your shell profile" ;;
      esac
      say "Add this to $rc, then restart your shell:"
      printf '\n      export PATH="%s:$PATH"\n\n' "$DEST"
      ;;
  esac
fi

printf '  %s\n\n' "------------------------------------------------------------"
say "Next, in any project you want coordinated:"
printf '\n'
say "  cd /path/to/your/project"
say "  $BIN setup        # detect your agent tools and configure them"
printf '\n'
dim "The dashboard runs at http://127.0.0.1:7801 once the daemon starts."
printf '\n'
