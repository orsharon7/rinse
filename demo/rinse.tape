# RINSE TUI demo — vhs tape
# Usage: vhs demo/rinse.tape
# Requirements:
#   - vhs installed (brew install vhs)
#   - rinse binary built and on PATH (make build && export PATH="$PWD:$PATH")
#   - A real GitHub PR accessible (set RINSE_DEMO_PR env var, e.g. "orsharon7/rinse#42")
#
# Output: website/demo.gif

Output website/demo.gif

Set Shell "bash"
Set FontSize 13
Set Width 900
Set Height 500
Set Theme "Catppuccin Macchiato"
Set TypingSpeed 80ms
Set Padding 20

# Start the demo
Type "rinse run --pr orsharon7/rinse#42"
Sleep 500ms
Enter

Sleep 2s

# TUI loads — cycle monitor
Sleep 3s

# Iteration 1 — fixing comments
Sleep 4s

# Iteration 2 — re-review
Sleep 4s

# Copilot approves
Sleep 2s

# Post-cycle summary visible
Sleep 3s

# End
Sleep 2s
