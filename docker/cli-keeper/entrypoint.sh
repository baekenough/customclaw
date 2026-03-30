#!/bin/bash
# cli-keeper entrypoint — token refresh + CLI auto-update sidecar
# Runs two independent background loops and waits for SIGTERM.

set -euo pipefail

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [cli-keeper] $*"
}

# ── Cleanup on SIGTERM ────────────────────────────────────────────────────────
shutdown() {
    log "SIGTERM received — shutting down"
    kill "${REFRESH_PID:-}" "${UPDATE_PID:-}" 2>/dev/null || true
    wait "${REFRESH_PID:-}" "${UPDATE_PID:-}" 2>/dev/null || true
    exit 0
}
trap shutdown SIGTERM SIGINT

# ── Token refresh loop ────────────────────────────────────────────────────────
# Interval default: 21600 s (6 hours) — well within the ~10 h token lifetime.
token_refresh_loop() {
    log "Token refresh loop started (interval: ${TOKEN_REFRESH_INTERVAL}s)"
    while true; do
        log "[refresh] Starting token refresh cycle"

        # ── Claude OAuth refresh ─────────────────────────────────────────────
        # ANTHROPIC_API_KEY must be unset so Claude CLI uses the OAuth token
        # from ~/.claude/.credentials.json (refreshToken flow).
        # Use env override prefix (ANTHROPIC_API_KEY=) so the unset persists
        # for the subprocess without affecting the current shell.
        if command -v claude &>/dev/null; then
            if ANTHROPIC_API_KEY= claude -p 'ping' --output-format text \
                      2>/dev/null | grep -q .; then
                log "[refresh] Claude OAuth token refreshed"
            else
                log "[refresh] WARN: Claude token refresh returned no output (token may still be valid)"
            fi
        else
            log "[refresh] WARN: claude binary not found — skipping"
        fi

        # ── Gemini OAuth refresh ─────────────────────────────────────────────
        # gemini stores OAuth creds at ~/.gemini/oauth_creds.json.
        if command -v gemini &>/dev/null; then
            if gemini --model gemini-2.0-flash -p 'ping' \
                      2>/dev/null | grep -q .; then
                log "[refresh] Gemini OAuth token refreshed"
            else
                log "[refresh] WARN: Gemini token refresh returned no output"
            fi
        else
            log "[refresh] INFO: gemini binary not found — skipping"
        fi

        # ── Codex (API-key based, no OAuth) ─────────────────────────────────
        # Codex uses OPENAI_API_KEY directly; no token refresh needed.
        # We still do a version check so the log shows it's alive.
        if command -v codex &>/dev/null; then
            CODEX_VER=$(codex --version 2>/dev/null || echo "unknown")
            log "[refresh] Codex present (${CODEX_VER}) — API-key auth, no refresh needed"
        else
            log "[refresh] INFO: codex binary not found — skipping"
        fi

        log "[refresh] Refresh cycle complete"

        sleep "${TOKEN_REFRESH_INTERVAL}" &
        wait $!  # interruptible sleep
    done
}

# ── CLI update loop ───────────────────────────────────────────────────────────
# Interval default: 86400 s (24 hours).
cli_update_loop() {
    log "CLI update loop started (interval: ${CLI_UPDATE_INTERVAL}s)"

    # Skip the very first run on boot to avoid competing with token refresh.
    sleep "${CLI_UPDATE_INTERVAL}" &
    wait $!

    while true; do
        log "[update] Starting CLI update cycle"

        # Update Claude Code and Codex
        if npm update -g @anthropic-ai/claude-code @openai/codex 2>&1 | \
           while IFS= read -r line; do log "[update:npm] ${line}"; done; then
            log "[update] npm update complete"
        else
            log "[update] WARN: npm update exited non-zero — continuing"
        fi

        # Attempt to update Gemini CLI if present
        if npm ls -g @google/gemini-cli &>/dev/null; then
            npm update -g @google/gemini-cli 2>&1 | \
                while IFS= read -r line; do log "[update:gemini] ${line}"; done || true
        fi

        # Log installed versions after update
        log "[update] Versions after update:"
        command -v claude  &>/dev/null && log "  claude:  $(claude  --version 2>/dev/null || echo 'err')"
        command -v codex   &>/dev/null && log "  codex:   $(codex   --version 2>/dev/null || echo 'err')"
        command -v gemini  &>/dev/null && log "  gemini:  $(gemini  --version 2>/dev/null || echo 'err')"

        log "[update] Update cycle complete"

        sleep "${CLI_UPDATE_INTERVAL}" &
        wait $!
    done
}

# ── Startup banner ────────────────────────────────────────────────────────────
log "cli-keeper starting"
log "Installed CLI versions:"
command -v claude &>/dev/null && log "  claude:  $(claude  --version 2>/dev/null || echo 'not found')"
command -v codex  &>/dev/null && log "  codex:   $(codex   --version 2>/dev/null || echo 'not found')"
command -v gemini &>/dev/null && log "  gemini:  $(gemini  --version 2>/dev/null || echo 'not found')"
log "TOKEN_REFRESH_INTERVAL: ${TOKEN_REFRESH_INTERVAL}s"
log "CLI_UPDATE_INTERVAL:    ${CLI_UPDATE_INTERVAL}s"

# ── Launch loops ──────────────────────────────────────────────────────────────
token_refresh_loop &
REFRESH_PID=$!

cli_update_loop &
UPDATE_PID=$!

log "Both loops running (refresh PID=${REFRESH_PID}, update PID=${UPDATE_PID})"

# Wait indefinitely — shutdown trap handles SIGTERM
wait
