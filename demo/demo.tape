# RINSE TUI Demo — records the PR review cycle for the landing page GIF
# Run: vhs demo/demo.tape
# Requires: vhs (brew install vhs), a working rinse binary

Output demo/demo.gif

Set Shell "bash"
Set FontSize 14
Set FontFamily "JetBrains Mono"
Set Width 900
Set Height 420
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms
Set LoopOffset 0%

# Simulate the rinse run --pr command with realistic output
# Uses a mock script so no live GitHub token is needed for demo recording

Sleep 500ms
Type "rinse run --pr 42"
Sleep 300ms
Enter
Sleep 800ms

# Iteration 1 — waiting for Copilot
Type ""
Sleep 1200ms

# Iteration 1 — fixing comments
Type ""
Sleep 1500ms

# Iteration 2 — waiting for Copilot
Type ""
Sleep 1200ms

# Approved!
Type ""
Sleep 3000ms
