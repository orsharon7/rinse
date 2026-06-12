package wait

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// emitTick writes a tick line to w in the exact format the TUI's
// extractWaitProgress() parses ("Copilot reviewing  ━━━━━─── 60s / 300s (20%)").
// The method tag (`webhook`|`poll`) is appended in parens so users can see which
// strategy is active without changing the parser contract.
func emitTick(w io.Writer, method string, elapsed, total time.Duration) {
	if w == nil {
		return
	}
	if total <= 0 {
		total = 1
	}
	elapsedSec := int(elapsed.Seconds())
	totalSec := int(total.Seconds())
	if elapsedSec > totalSec {
		elapsedSec = totalSec
	}
	pct := elapsedSec * 100 / totalSec
	const barW = 20
	filled := elapsedSec * barW / totalSec
	if filled > barW {
		filled = barW
	}
	empty := barW - filled
	bar := strings.Repeat("━", filled) + strings.Repeat("─", empty)
	fmt.Fprintf(w, "⏳ Copilot reviewing  %s  %ds / %ds (%d%%)  [%s]\n",
		bar, elapsedSec, totalSec, pct, method)
}
