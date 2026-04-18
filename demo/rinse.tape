# RINSE TUI demo — vhs tape
# Usage: vhs demo/rinse.tape
# Requirements:
#   vhs — install via: go install github.com/charmbracelet/vhs@latest
#          or:         brew install vhs
#   mock binary is self-contained — no Go build or GitHub auth needed.
#
# Output: website/demo.gif

Output website/demo.gif

Set Shell "bash"
Set FontSize 14
Set FontFamily "JetBrains Mono"
Set Width 900
Set Height 420
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms
Set Padding 20

# Put the mock binary first on PATH
Env PATH "./demo:$PATH"

# Show the prompt, then type the command
Sleep 500ms
Type "rinse run --pr 42"
Sleep 300ms
Enter

# Let the mock script run — it controls its own timing via sleep
# We just wait for it to finish (~18s of output)
Sleep 18s
