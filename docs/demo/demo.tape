# The README's GIF: a short tour. Run from the repository root: make demo
Output "docs/demo.gif"
Source docs/demo/settings.tape
Set Width 1400
Set Height 820
Set FontSize 14
Set Framerate 20
Set TypingSpeed 90ms

Hide
Type "unset NO_COLOR CI; rm -rf /tmp/paseo-tui-demo && mkdir -p /tmp/paseo-tui-demo && clear && HOME=/tmp/paseo-tui-demo ./paseo-tui -bridge docs/demo/demo-bridge.mjs -no-update-check"
Enter
Sleep 2.5s
Show

# an agent at work, live
Sleep 4s

# move through the sidebar to a finished one, with the details panel
Type "j"
Sleep 700ms
Type "j"
Sleep 1.2s
Type "d"
Sleep 2s

# verbose tool output: the edit's diff, highlighted
Type "v"
Sleep 3s
Type "v"
Sleep 300ms
Type "d"
Sleep 600ms

# the command center: jump to the agent that's waiting on a question
Type "p"
Sleep 600ms
Type "check"
Sleep 900ms
Enter
Sleep 2s

# answer it: two checks, then your own answer
Enter
Sleep 900ms
Space
Sleep 500ms
Down
Sleep 400ms
Down
Sleep 400ms
Space
Sleep 600ms
Tab
Sleep 600ms
Type "Block, and keep the cart"
Sleep 900ms
Ctrl+S
Sleep 4s

# slash commands and skills
Type "/"
Sleep 1.2s
Type "rev"
Sleep 1.2s
Backspace 4
Sleep 300ms
Escape
Sleep 300ms
Escape
Sleep 500ms

# settings, saved to config.toml as you go
Type "c"
Sleep 1.5s
Down
Sleep 400ms
Down
Sleep 400ms
Enter
Sleep 1.2s
Enter
Sleep 900ms
Escape
Sleep 1s

Hide
Type "q"
Type "q"
