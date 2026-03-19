Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 700
Set Padding 20

Sleep 500ms

# Launch prx
Type "prx"
Sleep 300ms
Enter

# Wait for PRs to load and first assessment to appear
Sleep 8s

# Show the assessment panel
Sleep 3s

# Tab to diff view
Tab
Sleep 1s

# Scroll down through the diff
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms

# Jump to next file
Type "]"
Sleep 1s

# Jump to next hunk
Type "}"
Sleep 1s
Type "}"
Sleep 1s

# Collapse a hunk
Left
Sleep 1s

# Expand it back
Right
Sleep 1s

# Navigate to next PR
Ctrl+N
Sleep 2s

# Show assessment for second PR
Sleep 2s

# Tab to diff
Tab
Sleep 1s

# Scroll through
Down
Sleep 200ms
Down
Sleep 200ms
Down
Sleep 200ms
Down
Sleep 200ms
Down
Sleep 200ms

Sleep 2s

# Quit
Type "q"
Sleep 1s
