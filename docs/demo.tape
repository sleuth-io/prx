Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 700
Set Padding 20

Sleep 500ms

# Launch prx against pulse repo
Type "prx ../pulse"
Sleep 300ms
Enter

# Wait for PRs to load
Sleep 4s

# Skip to third PR
Ctrl+N
Sleep 1s
Ctrl+N
Sleep 1s

# Tab to diff view
Tab
Sleep 1s

# Scroll down a few lines
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms

# Jump to a hunk
Type "}"
Sleep 1s

# Collapse it
Left
Sleep 1s

# Jump to next hunk
Type "}"
Sleep 1s

# Open chat and ask about blast radius
Type "?"
Sleep 5s
Type "Briefly explain the blast radius of this change"
Sleep 300ms
Enter

# Wait for Claude response
Sleep 30s
