#!/usr/bin/env bash
# termeownal — terminal Instagram DMs
# Copyright (C) 2026 termeownal contributors
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Second Phase 0 spike run: groups, edit/unsend, read receipts, message
# requests, a new DM to someone with no thread, and E2EE chats.
# Accounts: A = test account (cookies), B = your phone, C = a third account
# A has never messaged, D (optional) = a fourth account A doesn't follow.
# Usage: spike/run2.sh [COOKIES_PATH]   (default ~/ig-test/cookies.json)
# Output (IDs only, no message text) goes to ~/ig-test/spike-output-2.txt.
set -uo pipefail

C="${1:-$HOME/ig-test/cookies.json}"
OUT="$HOME/ig-test/spike-output-2.txt"
cd "$(dirname "$0")/.." || exit 1
go build -o bin/spike ./spike || exit 1
: >"$OUT"

run() { echo; echo "\$ spike $*" | tee -a "$OUT"; bin/spike -cookies "$C" "$@" 2>&1 | tee -ia "$OUT"; }  # -i: Ctrl+C ends listen, not the log
pause() { echo; read -rp ">>> $1 Press Enter when done. "; echo "[human: $1]" >>"$OUT"; }
ask() { read -rp "$1 " REPLY; echo "[human: $1 $REPLY]" >>"$OUT"; }

pause "On phone (B): create a group with A and C. On B, close the 1:1 chat with A."
run index
echo
read -rp "1:1 thread with B — id= (short): " SHORT
read -rp "Group — id= (short): " GSHORT
read -rp "Group — thread_id= (long): " GLONG
read -rp "fbid= of C (from the group's user list): " CFBID

run send "$SHORT" "spike read receipt test"
run history "$GSHORT"
run send "$GSHORT" "spike group test 1 (short id)"
run -long send "$GLONG" "spike group test 2 (long id)"
ask "Did both group test messages arrive? (y/n)"

cat <<'EOF'

>>> listen runs 5 minutes. From the phones, in this order:
    B, 1:1 chat:  open it (read receipt), send a message, edit it, unsend it
    B, group:     send a message, react to one, edit yours, unsend it, rename the group
    B:            start an end-to-end encrypted chat with A (if offered) and send a message
    D (optional): send A a message (message request)
EOF
run listen 5m
ask "Which of those steps did you do? (e.g. all / all but D / no E2EE option)"

run index                 # E2EE chat, group rename, message request visible?
run folder PENDING        # message requests

run newdm "$CFBID"        # expect ErrThreadNotFound
run send-new "$CFBID" "spike new dm test"
ask "Did C receive the new DM? (y/n)"
run newdm "$CFBID"        # expect long + short now

echo; echo "Done. Output saved to $OUT"
