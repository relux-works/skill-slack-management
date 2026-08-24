#!/usr/bin/env zsh

set -euo pipefail

SKILL_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BINARY_NAME="slack-mgmt"
BUILD_OUTPUT="$SKILL_DIR/$BINARY_NAME"
BIN_DIR="${SLACK_MGMT_BIN_DIR:-$HOME/.local/bin}"
INSTALL_ONLY="${SLACK_MGMT_INSTALL_ONLY:-0}"
WITH_ATTACHMENTS="${SLACK_MGMT_WITH_ATTACHMENTS:-0}"
WITH_BROWSER="${SLACK_MGMT_WITH_BROWSER:-0}"
WITH_SLACKDUMP="${SLACK_MGMT_WITH_SLACKDUMP:-0}"
CONFIG_DIR="${SLACK_MGMT_CONFIG_DIR:-$HOME/Library/Application Support/slack-mgmt}"
INSTALL_STATE_PATH="$CONFIG_DIR/install.json"
AGENTS_DEST="$HOME/.agents/skills/slack-management"
CLAUDE_DEST="$HOME/.claude/skills/slack-management"
CODEX_DEST="$HOME/.codex/skills/slack-management"
BUILD_VERSION="dev"
BUILD_COMMIT="unknown"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
BUILD_LDFLAGS=""

green() { print -P "%F{green}$1%f"; }
yellow() { print -P "%F{yellow}$1%f"; }
red() { print -P "%F{red}$1%f"; }

json_escape() {
  print -rn -- "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

usage() {
  cat <<EOF
Usage: scripts/setup.sh [options]

Options:
  --bin-dir PATH       Install binary into PATH (default: $HOME/.local/bin)
  --install-only       Safe reinstall of binary, skill artifact, links, and install state
  --with-attachments   Install the agents-attachments bridge (requires agents-infra)
  --with-browser       Require and verify the optional mac-chrome-session transport
  --with-slackdump     Require and verify the optional Slackdump v4 provider
  --help, -h           Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin-dir)
      BIN_DIR="$2"
      shift 2
      ;;
    --install-only)
      INSTALL_ONLY="1"
      shift
      ;;
    --with-attachments)
      WITH_ATTACHMENTS="1"
      shift
      ;;
    --with-browser)
      WITH_BROWSER="1"
      shift
      ;;
    --with-slackdump)
      WITH_SLACKDUMP="1"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      red "Unknown option: $1"
      usage
      exit 1
      ;;
  esac
done

install_go() {
  if command -v go >/dev/null 2>&1; then
    green "Go already installed: $(go version)"
    return
  fi

  if ! command -v brew >/dev/null 2>&1; then
    red "Go is missing and Homebrew is not available. Install Homebrew or Go first."
    exit 1
  fi

  yellow "Go not found. Installing via Homebrew..."
  brew install go
  green "Go installed: $(go version)"
}

compute_ldflags() {
  if git -C "$SKILL_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    BUILD_VERSION="$(git -C "$SKILL_DIR" describe --tags --always 2>/dev/null || echo "dev")"
    BUILD_COMMIT="$(git -C "$SKILL_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
  fi

  BUILD_LDFLAGS="-X main.Version=$BUILD_VERSION -X main.Commit=$BUILD_COMMIT -X main.BuildDate=$BUILD_DATE"
}

build_cli() {
  green "Building $BINARY_NAME ..."
  (
    cd "$SKILL_DIR"
    go build -trimpath -ldflags "$BUILD_LDFLAGS" -o "$BUILD_OUTPUT" ./cmd/slack-mgmt
  )
  green "Built: $BUILD_OUTPUT"
}

install_binary() {
  local dest="$BIN_DIR/$BINARY_NAME"
  local tmp="$dest.tmp.$$"
  mkdir -p "$BIN_DIR"
  cp "$BUILD_OUTPUT" "$tmp"
  chmod +x "$tmp"
  xattr -c "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$dest"
  green "Installed binary: $dest"
}

scrub_git_metadata() {
  local dir="$1"
  [[ -d "$dir" ]] || return
  find "$dir" -depth \( -name .git -o -name .gitignore -o -name .gitattributes -o -name .gitmodules \) -exec rm -rf {} +
}

install_skill_artifact() {
  mkdir -p "$AGENTS_DEST"
  rsync -a --delete --delete-excluded "$SKILL_DIR/" "$AGENTS_DEST/" \
    --exclude='.git' \
    --exclude='.gitignore' \
    --exclude='.gitattributes' \
    --exclude='.gitmodules' \
    --exclude='/AGENTS.md' \
    --exclude='/.temp' \
    --exclude='/.task-board' \
    --exclude='/.agents' \
    --exclude='/.claude' \
    --exclude='/.codex' \
    --exclude='/.local' \
    --exclude='/.planning' \
    --exclude='/.research' \
    --exclude='/.spec' \
    --exclude='/task-board.config.json' \
    --exclude='/LOGBOOK.md' \
    --exclude='/scripts/verify-agent-safety-contract.sh' \
    --exclude='/scripts/verify-agent-safety-contract-test.sh' \
    --exclude='/dist' \
    --exclude='/slack-mgmt' \
    --exclude='/slack-mgmt.exe'
  scrub_git_metadata "$AGENTS_DEST"
  green "Installed skill artifact: $AGENTS_DEST"
}

install_optional_integrations() {
  if [[ "$WITH_ATTACHMENTS" == "1" ]]; then
    if ! command -v agents-infra >/dev/null 2>&1; then
      red "--with-attachments requires agents-infra on PATH."
      red "Install relux-agents-infra, then rerun setup."
      exit 1
    fi
    local dest="$BIN_DIR/agents-attachments"
    local tmp="$dest.tmp.$$"
    cat > "$tmp" <<'SH'
#!/usr/bin/env sh
set -eu
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -x "$DIR/agents-infra" ]; then
  TARGET="$DIR/agents-infra"
else
  TARGET=agents-infra
fi
STATUS_FILE=$(mktemp "${TMPDIR:-/tmp}/agents-attachments.XXXXXX")
trap 'rm -f "$STATUS_FILE"' EXIT HUP INT TERM
if "$TARGET" attachments "$@" 2>"$STATUS_FILE"; then
  STATUS=0
else
  STATUS=$?
fi
MAPPED_STATUS=$(sed -n '$s/^exit status \([0-9][0-9]*\)$/\1/p' "$STATUS_FILE")
if [ -n "$MAPPED_STATUS" ]; then
  sed '$d' "$STATUS_FILE" >&2
  exit "$MAPPED_STATUS"
fi
cat "$STATUS_FILE" >&2
exit "$STATUS"
SH
    chmod +x "$tmp"
    mv -f "$tmp" "$dest"
    green "Installed optional attachment bridge: $dest"
  fi

  if [[ "$WITH_BROWSER" == "1" ]]; then
    if [[ "$(uname -s)" != "Darwin" ]]; then
      red "--with-browser is supported only on macOS."
      exit 1
    fi
    if ! command -v mac-chrome-session >/dev/null 2>&1; then
      red "--with-browser requires mac-chrome-session from mac-infra on PATH."
      exit 1
    fi
    mac-chrome-session version >/dev/null
    green "Verified optional browser transport: $(command -v mac-chrome-session)"
  fi

  if [[ "$WITH_SLACKDUMP" == "1" ]]; then
    if ! command -v slackdump >/dev/null 2>&1; then
      red "--with-slackdump requires an official Slackdump v4 executable on PATH."
      exit 1
    fi
    local slackdump_version
    slackdump_version="$(slackdump version 2>&1)"
    if [[ ! "$slackdump_version" =~ '(^|[^0-9])v?4\.' ]]; then
      red "Unsupported Slackdump version: $slackdump_version (expected v4)."
      exit 1
    fi
    green "Verified optional Slackdump provider: $slackdump_version"
  fi
}

refresh_links() {
  mkdir -p "$HOME/.claude/skills" "$HOME/.codex/skills"
  rm -rf "$CLAUDE_DEST" "$CODEX_DEST"
  ln -s "$AGENTS_DEST" "$CLAUDE_DEST"
  ln -s "$AGENTS_DEST" "$CODEX_DEST"
  green "Refreshed Claude/Codex skill links"
}

write_install_state() {
  mkdir -p "$CONFIG_DIR"
  local escaped_repo escaped_skill escaped_bin escaped_platform escaped_arch escaped_version escaped_commit escaped_build_date
  escaped_repo="$(json_escape "$SKILL_DIR")"
  escaped_skill="$(json_escape "$AGENTS_DEST")"
  escaped_bin="$(json_escape "$BIN_DIR")"
  escaped_platform="$(json_escape "$(uname -s | tr '[:upper:]' '[:lower:]')")"
  escaped_arch="$(json_escape "$(uname -m)")"
  escaped_version="$(json_escape "$BUILD_VERSION")"
  escaped_commit="$(json_escape "$BUILD_COMMIT")"
  escaped_build_date="$(json_escape "$BUILD_DATE")"
  cat > "$INSTALL_STATE_PATH" <<EOF
{
  "repoPath": "$escaped_repo",
  "installedSkillPath": "$escaped_skill",
  "binDir": "$escaped_bin",
  "platform": "$escaped_platform",
  "arch": "$escaped_arch",
  "version": "$escaped_version",
  "commit": "$escaped_commit",
  "buildDate": "$escaped_build_date",
  "installOnly": $([[ "$INSTALL_ONLY" == "1" ]] && echo "true" || echo "false"),
  "attachmentsEnabled": $([[ "$WITH_ATTACHMENTS" == "1" ]] && echo "true" || echo "false"),
  "browserEnabled": $([[ "$WITH_BROWSER" == "1" ]] && echo "true" || echo "false"),
  "slackdumpEnabled": $([[ "$WITH_SLACKDUMP" == "1" ]] && echo "true" || echo "false")
}
EOF
  green "Install state: $INSTALL_STATE_PATH"
}

verify_install() {
  local dest="$BIN_DIR/$BINARY_NAME"
  [[ -x "$dest" ]] || { red "Missing installed binary: $dest"; exit 1; }
  [[ -f "$AGENTS_DEST/SKILL.md" ]] || { red "Installed skill artifact is missing SKILL.md"; exit 1; }
  [[ -f "$AGENTS_DEST/LICENSE" ]] || { red "Installed skill artifact is missing LICENSE"; exit 1; }
  [[ -f "$CLAUDE_DEST/SKILL.md" ]] || { red "Claude skill link is not usable: $CLAUDE_DEST"; exit 1; }
  [[ -f "$CODEX_DEST/SKILL.md" ]] || { red "Codex skill link is not usable: $CODEX_DEST"; exit 1; }
  if find "$AGENTS_DEST" \( -name .git -o -name .gitignore -o -name .gitattributes -o -name .gitmodules \) -print -quit | grep -q .; then
    red "Installed skill artifact still contains Git metadata"
    exit 1
  fi
  if [[ "$WITH_ATTACHMENTS" == "1" ]]; then
    [[ -x "$BIN_DIR/agents-attachments" ]] || { red "Missing attachment bridge"; exit 1; }
    agents-infra version >/dev/null
  fi

  local resolved=""
  if resolved="$(command -v "$BINARY_NAME" 2>/dev/null)"; then
    if [[ "$resolved" != "$dest" ]]; then
      red "$BINARY_NAME on PATH resolves to $resolved"
      red "Expected: $dest"
      exit 1
    fi
  else
    yellow "$BIN_DIR is not in PATH yet."
    yellow "Add to ~/.zshrc: export PATH=\"\$HOME/.local/bin:\$PATH\""
  fi

  "$dest" version >/dev/null
  "$dest" q 'schema()' --format json >/dev/null
  "$dest" q 'provider_capabilities()' --format json >/dev/null
  "$dest" m 'schema()' --format json >/dev/null
  "$dest" auth config-path >/dev/null
  green "Verified binary and skill artifact"
}

print ""
green "=== slack-management setup ==="
print ""
if [[ "$INSTALL_ONLY" == "1" ]]; then
  yellow "Running safe reinstall flow (--install-only)"
fi
install_go
compute_ldflags
build_cli
install_binary
install_skill_artifact
install_optional_integrations
refresh_links
write_install_state
verify_install
print ""
green "=== Done ==="
