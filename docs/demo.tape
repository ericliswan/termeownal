# Records the README demo against the fake backend, so no real account,
# username or message ever shows up in it.
#
#   make demo          # or: make static && vhs docs/demo.tape
#
# Needs vhs (https://github.com/charmbracelet/vhs). Writes assets/demo.gif.

Output assets/demo.gif

Set Shell "bash"
Set FontSize 16
Set FontFamily "JetBrains Mono"
Set Width 1200
Set Height 760
Set Padding 20
Set Margin 24
Set MarginFill "#0A0A0A"
Set WindowBar Colorful
Set BorderRadius 12
Set TypingSpeed 70ms
Set Theme { "name": "termeownal", "background": "#0E0E0E", "foreground": "#E5E5E5", "cursor": "#F5F5F5", "selection": "#333333", "black": "#0E0E0E", "red": "#F87171", "green": "#4ADE80", "yellow": "#FBBF24", "blue": "#93C5FD", "magenta": "#C084FC", "cyan": "#67E8F9", "white": "#E5E5E5", "brightBlack": "#6B7280", "brightRed": "#FCA5A5", "brightGreen": "#86EFAC", "brightYellow": "#FDE68A", "brightBlue": "#BFDBFE", "brightMagenta": "#D8B4FE", "brightCyan": "#A5F3FC", "brightWhite": "#FFFFFF" }

Hide
Type "clear" Enter
Show

# Start against canned chats.
Type "./bin/meow --backend=fake"
Sleep 600ms
Enter
Sleep 2s

# Move down a thread and open it.
Type "j"
Sleep 700ms
Enter
Sleep 2s

# Send a message; the fake echoes it back after a moment.
Type "omw, bringing tuna"
Sleep 500ms
Enter
Sleep 2s

# Back to the thread list and browse a little.
Escape
Sleep 700ms
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms
Type "k"
Sleep 900ms

# Show the help overlay.
Type "?"
Sleep 2.5s
Escape
Sleep 1s

Ctrl+C
Sleep 500ms
