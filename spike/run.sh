#!/usr/bin/env bash
# termeownal — terminal Instagram DMs
# Copyright (C) 2026 termeownal contributors
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs the whole Phase 0 spike in order, pausing for phone steps.
# Usage: spike/run.sh [COOKIES_PATH]   (default ~/ig-test/cookies.json)
# Output (IDs only, no message text) goes to ~/ig-test/spike-output.txt.
set -uo pipefail

C="${1:-$HOME/ig-test/cookies.json}"
S="$HOME/.termeownal-spike"
OUT="$HOME/ig-test/spike-output.txt"
cd "$(dirname "$0")/.." || exit 1
go build -o bin/spike ./spike || exit 1
: >"$OUT"

run() { echo; echo "\$ spike $*" | tee -a "$OUT"; bin/spike -cookies "$C" "$@" 2>&1 | tee -a "$OUT"; }
pause() { echo; read -rp ">>> $1 Press Enter when done. "; echo "[human: $1]" >>"$OUT"; }

run index
echo
read -rp "Pick a thread you can message from your phone. Its id= (short): " SHORT
read -rp "Its thread_id= (long): " LONG
read -rp "fbid= of the other user in that thread: " FBID

run history "$SHORT"
run send "$SHORT" "spike test 1 (short id)"
run -long send "$LONG" "spike test 2 (long id)"
read -rp "Did both test messages arrive on the phone? (y/n) " A; echo "[human: both test messages arrived: $A]" >>"$OUT"

echo; echo ">>> listen runs 3 minutes. From the phone: send a message, react, edit, unsend, open the chat."
run listen 3m

pause "From the phone, send one more message to the test account and leave it unread."
run markread "$SHORT"
read -rp "Does the phone now show that message as seen? (y/n) " A; echo "[human: phone shows seen: $A]" >>"$OUT"

rm -f "$S/state.json"; run history "$SHORT" 3            # expect WARM
rm -f "$S/state.json" "$S/seq.json"; run history "$SHORT" 3  # expect COLD
run history "$SHORT" 3                                   # expect FAST

run newdm "$FBID"

echo; echo "Done. Output saved to $OUT"
