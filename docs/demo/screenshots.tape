# The README's screenshots. Run from the repository root: make screenshots
Output "docs/demo/.screenshots.gif"
Source docs/demo/settings.tape

Hide
Type "unset NO_COLOR CI; rm -rf /tmp/paseo-tui-demo && mkdir -p /tmp/paseo-tui-demo && clear && HOME=/tmp/paseo-tui-demo ./paseo-tui -bridge docs/demo/demo-bridge.mjs -no-update-check"
Enter
Sleep 3s
Show

# the main view: a finished agent with the details panel and verbose tool output (v)
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "d"
Sleep 300ms
Type "v"
Sleep 1s
Screenshot "docs/screenshot-main.png"
Sleep 500ms
Type "v"
Sleep 300ms

# the command center, searching
Type "p"
Sleep 400ms
Type "check"
Sleep 800ms
Screenshot "docs/screenshot-palette.png"
Sleep 500ms
Enter
Sleep 1s

# a pending question, then its form with answers being picked
Enter
Sleep 500ms
Space
Sleep 200ms
Down
Sleep 200ms
Space
Sleep 200ms
Tab
Sleep 200ms
Type "Block, and keep the cart"
Sleep 600ms
Screenshot "docs/screenshot-question.png"
Sleep 500ms
Escape
Sleep 500ms

# slash commands
Type "j"
Sleep 300ms
Type "/"
Sleep 800ms
Screenshot "docs/screenshot-slash.png"
Sleep 500ms
Backspace
Sleep 200ms
Escape
Sleep 200ms
Escape
Sleep 300ms

# settings
Type "c"
Sleep 600ms
Screenshot "docs/screenshot-settings.png"
Sleep 500ms
Escape
Sleep 300ms

# New workspace
Type "N"
Sleep 800ms
Type "Add an order history page with filters for date and status"
Sleep 500ms
Screenshot "docs/screenshot-new.png"
Sleep 500ms
Escape
Sleep 300ms
Type "q"
Type "q"
