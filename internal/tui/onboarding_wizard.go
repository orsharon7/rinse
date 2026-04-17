package tui

// onboarding_wizard.go — First-run TUI wizard (Steps A–E) for RINSE v0.2.
//
// This file implements a standalone bubbletea model that runs before the main
// PR picker TUI whenever onboarding has not been completed. It handles:
//   - Splash / loading with animated spinner
//   - Resume banner (if partial state exists from a previous incomplete run)
//   - Welcome screen with "Get started" / "Skip setup" options
//   - Step A — Overview (3 bullets)
//   - Step B — Name your first cycle (text input + suggestion chips)
//   - Step C — Pick defaults (3 toggle cards)
//   - Step D — First cycle preview (POST /cycles)
//   - Step E — Completion animation or static fallback ($NO_COLOR aware)
//
// Run it via RunOnboardingWizard(). The function returns after the wizard
// completes so the caller can proceed to the main TUI.

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/orsharon7/rinse/internal/onboarding"
	"github.com/orsharon7/rinse/internal/theme"
)

// ── View modes ────────────────────────────────────────────────────────────────

type wizView int

const (
	wizSplash   wizView = iota
	wizResume           // resume banner (partial state found)
	wizWelcome          // welcome screen
	wizStepA            // overview
	wizStepB            // name cycle
	wizStepC            // pick defaults
	wizStepD            // first cycle preview + POST /cycles
	wizStepE            // completion / celebration
	wizDone             // wizard finished, proceed to main UI
)

// ── Internal messages ─────────────────────────────────────────────────────────

type wizSplashDoneMsg struct{}

type wizCycleCreatedMsg struct {
	cycleID   string
	cycleName string
}

type wizCycleErrMsg struct{ err error }

type wizConfigWrittenMsg struct{}

type wizConfigErrMsg struct{ err error }

type wizCelebFrameMsg struct{} // tick for Step E animation frames

// ── Wizard result ─────────────────────────────────────────────────────────────

// WizardOutcome describes what happened when the wizard finished.
type WizardOutcome int

const (
	WizardCompleted WizardOutcome = iota // steps A–E done
	WizardSkipped                        // user chose "Skip setup"
	WizardAborted                        // user quit (ctrl+c / q)
)

// ── Wizard model ──────────────────────────────────────────────────────────────

type wizModel struct {
	width  int
	height int

	view    wizView
	outcome WizardOutcome

	// splash
	sp        spinner.Model
	spReady   bool
	savedStep onboarding.Step // from loaded state (may be "")

	// resume
	resumeChoice int // 0 = pick up, 1 = start over

	// welcome
	welcomeChoice int // 0 = get started, 1 = skip setup

	// step B
	cycleInput     textinput.Model
	cycleInputErr  string
	chipIdx        int
	chipSelected   bool

	// step C
	remindOnComplete bool
	autoAdvance      bool
	saveHistory      bool
	cFocus           int // 0=toggle1, 1=toggle2, 2=toggle3, 3=next, 4=skip

	// step D
	cycleName     string
	creatingCycle bool
	cycleErr      string
	createdID     string

	// step E
	colorOK      bool // true if ANSI color is safe to render
	celebFrame   int
	celebDone    bool
	celebFrames  []string
}

// suggestion chips for Step B (from copy deck)
var cycleChips = []string{"Weekly laundry", "Delicates", "Bedding", "Quick wash"}

// ── Init ──────────────────────────────────────────────────────────────────────

func newWizModel() wizModel {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(theme.Mauve)

	ti := textinput.New()
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(theme.Mauve)
	ti.PromptStyle = lipgloss.NewStyle().Foreground(theme.Mauve)
	ti.TextStyle = lipgloss.NewStyle().Foreground(theme.Text)
	ti.Prompt = "  " + theme.IconArrow + " "
	ti.CharLimit = 50
	ti.Placeholder = "e.g. Weekly laundry"

	// Check color support: $NO_COLOR trumps everything.
	colorOK := os.Getenv("NO_COLOR") == ""
	if colorOK {
		term := os.Getenv("TERM")
		if term == "dumb" || term == "" {
			colorOK = false
		}
	}

	return wizModel{
		view:             wizSplash,
		sp:               sp,
		cycleInput:       ti,
		remindOnComplete: true,
		autoAdvance:      false,
		saveHistory:      true,
		colorOK:          colorOK,
		celebFrames:      buildCelebFrames(colorOK),
	}
}

// buildCelebFrames returns the Step E animation frame sequence.
// If color is not supported, returns a single static fallback frame.
func buildCelebFrames(colorOK bool) []string {
	if !colorOK {
		return []string{"  ✓  You are in!"}
	}
	colors := []lipgloss.Color{
		lipgloss.Color("#4F6EF7"),
		lipgloss.Color("#8BD5CA"),
		lipgloss.Color("#A6DA95"),
		lipgloss.Color("#27AE60"),
	}
	frames := make([]string, len(colors))
	for i, c := range colors {
		frames[i] = lipgloss.NewStyle().Foreground(c).Bold(true).Render("  ✓  You are in!")
	}
	return frames
}

func (m wizModel) Init() tea.Cmd {
	return tea.Batch(
		m.sp.Tick,
		tea.Tick(900*time.Millisecond, func(t time.Time) tea.Msg { return wizSplashDoneMsg{} }),
	)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m wizModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		if m.view == wizSplash {
			var cmd tea.Cmd
			m.sp, cmd = m.sp.Update(msg)
			return m, cmd
		}
		return m, nil

	case wizSplashDoneMsg:
		m.spReady = true
		return m.advanceFromSplash()

	case wizCycleCreatedMsg:
		m.creatingCycle = false
		m.createdID = msg.cycleID
		m.cycleName = msg.cycleName
		// Save step D, then move to E
		go func() {
			s := onboarding.State{
				Version:        onboarding.StateVersion,
				CompletedStep:  onboarding.StepD,
				CycleNameDraft: m.cycleName,
				Defaults: onboarding.Defaults{
					RemindOnComplete: m.remindOnComplete,
					AutoAdvance:      m.autoAdvance,
					SaveHistory:      m.saveHistory,
				},
			}
			_ = onboarding.SaveState(s)
		}()
		m.view = wizStepE
		if len(m.celebFrames) > 1 {
			return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return wizCelebFrameMsg{} })
		}
		return m, nil

	case wizCycleErrMsg:
		m.creatingCycle = false
		m.cycleErr = friendlyCycleErr(msg.err)
		return m, nil

	case wizConfigWrittenMsg:
		m.view = wizStepD
		return m, nil

	case wizConfigErrMsg:
		// Config write failed — show inline error but do not advance (per spec).
		m.cycleErr = "Could not save settings: " + msg.err.Error() + ". Please try again."
		return m, nil

	case wizCelebFrameMsg:
		if m.view == wizStepE && !m.celebDone {
			m.celebFrame++
			if m.celebFrame >= len(m.celebFrames) {
				m.celebFrame = len(m.celebFrames) - 1
				m.celebDone = true
			} else {
				return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return wizCelebFrameMsg{} })
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Forward to text input when active.
	if m.view == wizStepB {
		var cmd tea.Cmd
		m.cycleInput, cmd = m.cycleInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m wizModel) advanceFromSplash() (wizModel, tea.Cmd) {
	state, err := onboarding.LoadState()
	if err != nil || state == nil {
		// No state file → fresh start
		m.view = wizWelcome
		return m, nil
	}
	if state.Skipped || state.CompletedStep == onboarding.StepE {
		// Already complete — should not happen (caller checks), but handle gracefully.
		m.view = wizDone
		return m, tea.Quit
	}
	// Partial progress — show resume banner.
	m.savedStep = state.CompletedStep
	// Restore previously entered cycle name.
	m.cycleName = state.CycleNameDraft
	// Only restore defaults if the user has already chosen them in Step C or later.
	// Before Step C, the defaults in the state file are zero values (not user selections).
	switch state.CompletedStep {
	case onboarding.StepC, onboarding.StepD, onboarding.StepE:
		m.remindOnComplete = state.Defaults.RemindOnComplete
		m.autoAdvance = state.Defaults.AutoAdvance
		m.saveHistory = state.Defaults.SaveHistory
	}
	m.view = wizResume
	return m, nil
}

func (m wizModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.outcome = WizardAborted
		return m, tea.Quit
	}

	switch m.view {
	case wizSplash:
		// Any key skips the splash timer.
		if m.spReady {
			return m.advanceFromSplash()
		}
		return m, nil

	case wizResume:
		return m.handleResumeKey(msg)

	case wizWelcome:
		return m.handleWelcomeKey(msg)

	case wizStepA:
		return m.handleStepAKey(msg)

	case wizStepB:
		return m.handleStepBKey(msg)

	case wizStepC:
		return m.handleStepCKey(msg)

	case wizStepD:
		return m.handleStepDKey(msg)

	case wizStepE:
		return m.handleStepEKey(msg)
	}

	return m, nil
}

// ── Resume banner keys ────────────────────────────────────────────────────────

func (m wizModel) handleResumeKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.resumeChoice = 0
	case "down", "j":
		m.resumeChoice = 1
	case "1":
		m.resumeChoice = 0
		return m.doResume()
	case "2":
		m.resumeChoice = 1
		return m.doStartOver()
	case "enter":
		if m.resumeChoice == 0 {
			return m.doResume()
		}
		return m.doStartOver()
	case "q":
		m.outcome = WizardAborted
		return m, tea.Quit
	}
	return m, nil
}

func (m wizModel) doResume() (wizModel, tea.Cmd) {
	// Navigate to the step after the last completed one.
	switch m.savedStep {
	case onboarding.StepNone:
		m.view = wizStepA
	case onboarding.StepA:
		m.view = wizStepB
		m.cycleInput.Focus()
		m.cycleInput.SetValue(m.cycleName)
		return m, textinput.Blink
	case onboarding.StepB:
		m.view = wizStepC
	case onboarding.StepC:
		m.view = wizStepD
	case onboarding.StepD:
		m.view = wizStepE
		if len(m.celebFrames) > 1 {
			return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return wizCelebFrameMsg{} })
		}
	default:
		m.view = wizWelcome
	}
	return m, nil
}

func (m wizModel) doStartOver() (wizModel, tea.Cmd) {
	// Delete state and restart fresh.
	_ = onboarding.DeleteState()
	m.cycleName = ""
	m.cycleInput.SetValue("")
	m.remindOnComplete = true
	m.autoAdvance = false
	m.saveHistory = true
	m.savedStep = onboarding.StepNone
	m.view = wizWelcome
	return m, nil
}

// ── Welcome keys ──────────────────────────────────────────────────────────────

func (m wizModel) handleWelcomeKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.welcomeChoice = 0
	case "down", "j":
		m.welcomeChoice = 1
	case "enter":
		if m.welcomeChoice == 0 {
			// Get started → Step A
			saveStepAsync(onboarding.State{Version: onboarding.StateVersion})
			m.view = wizStepA
		} else {
			// Skip setup
			_ = onboarding.SaveState(onboarding.State{
				Version: onboarding.StateVersion,
				Skipped: true,
			})
			m.outcome = WizardSkipped
			return m, tea.Quit
		}
	case "q":
		m.outcome = WizardAborted
		return m, tea.Quit
	}
	return m, nil
}

// ── Step A keys ───────────────────────────────────────────────────────────────

func (m wizModel) handleStepAKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	switch msg.String() {
	case "enter", " ":
		// "Sounds good, let me try it" or "Skip intro" — both go to Step B
		saveStepAsync(onboarding.State{
			Version:       onboarding.StateVersion,
			CompletedStep: onboarding.StepA,
		})
		m.view = wizStepB
		m.cycleInput.Focus()
		if m.cycleName != "" {
			m.cycleInput.SetValue(m.cycleName)
		}
		return m, textinput.Blink
	case "q":
		m.outcome = WizardAborted
		return m, tea.Quit
	}
	return m, nil
}

// ── Step B keys ───────────────────────────────────────────────────────────────

func (m wizModel) handleStepBKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = wizStepA
		m.cycleInput.Blur()
		return m, nil

	case "tab":
		// Cycle suggestion chips
		m.chipIdx = (m.chipIdx + 1) % len(cycleChips)
		m.chipSelected = true
		m.cycleInput.SetValue(cycleChips[m.chipIdx])
		m.cycleInputErr = ""
		return m, nil

	case "enter":
		val := strings.TrimSpace(m.cycleInput.Value())
		if val == "" {
			m.cycleInputErr = "Give it a name so you can find it easily."
			return m, nil
		}
		if utf8.RuneCountInString(val) > 50 {
			m.cycleInputErr = "Keep it under 50 characters."
			return m, nil
		}
		m.cycleInputErr = ""
		m.cycleName = val
		saveStepAsync(onboarding.State{
			Version:        onboarding.StateVersion,
			CompletedStep:  onboarding.StepB,
			CycleNameDraft: m.cycleName,
		})
		m.cycleInput.Blur()
		m.view = wizStepC
		m.cFocus = 0
		return m, nil

	case "q":
		m.outcome = WizardAborted
		return m, tea.Quit
	}

	// Forward remaining keys to the text input.
	var cmd tea.Cmd
	m.cycleInput, cmd = m.cycleInput.Update(msg)
	// Clear error on any text change.
	if m.cycleInputErr != "" {
		m.cycleInputErr = ""
	}
	return m, cmd
}

// ── Step C keys ───────────────────────────────────────────────────────────────

const (
	cFocusRemind = 0
	cFocusAuto   = 1
	cFocusHist   = 2
	cFocusNext   = 3
	cFocusSkip   = 4
)

func (m wizModel) handleStepCKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = wizStepB
		m.cycleInput.Focus()
		return m, textinput.Blink

	case "up", "k":
		if m.cFocus > 0 {
			m.cFocus--
		}
	case "down", "j", "tab":
		if m.cFocus < cFocusSkip {
			m.cFocus++
		}

	case " ":
		switch m.cFocus {
		case cFocusRemind:
			m.remindOnComplete = !m.remindOnComplete
		case cFocusAuto:
			m.autoAdvance = !m.autoAdvance
		case cFocusHist:
			m.saveHistory = !m.saveHistory
		}

	case "enter":
		switch m.cFocus {
		case cFocusRemind:
			m.remindOnComplete = !m.remindOnComplete
		case cFocusAuto:
			m.autoAdvance = !m.autoAdvance
		case cFocusHist:
			m.saveHistory = !m.saveHistory
		case cFocusNext, cFocusSkip:
			// "Skip for now" uses defaults (already set); both write config.
			d := onboarding.Defaults{
				RemindOnComplete: m.remindOnComplete,
				AutoAdvance:      m.autoAdvance,
				SaveHistory:      m.saveHistory,
			}
			saveStepAsync(onboarding.State{
				Version:        onboarding.StateVersion,
				CompletedStep:  onboarding.StepC,
				CycleNameDraft: m.cycleName,
				Defaults:       d,
			})
			cycleName := m.cycleName
			return m, func() tea.Msg {
				if err := onboarding.WriteTomlConfig(cycleName, d); err != nil {
					return wizConfigErrMsg{err}
				}
				return wizConfigWrittenMsg{}
			}
		}

	case "q":
		m.outcome = WizardAborted
		return m, tea.Quit
	}

	return m, nil
}

// ── Step D keys ───────────────────────────────────────────────────────────────

func (m wizModel) handleStepDKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	if m.creatingCycle {
		if msg.String() == "ctrl+c" {
			m.outcome = WizardAborted
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "a": // "Adjust first" → back to Step C
		m.view = wizStepC
		m.cFocus = 0
		m.cycleErr = ""
		return m, nil

	case "enter", "s": // "Start cycle"
		m.creatingCycle = true
		m.cycleErr = ""
		cycleName := m.cycleName
		d := onboarding.Defaults{
			RemindOnComplete: m.remindOnComplete,
			AutoAdvance:      m.autoAdvance,
			SaveHistory:      m.saveHistory,
		}
		return m, func() tea.Msg {
			cycle, err := onboarding.CreateCycle(cycleName, d)
			if err != nil {
				return wizCycleErrMsg{err}
			}
			return wizCycleCreatedMsg{cycleID: cycle.ID, cycleName: cycle.Name}
		}

	case "q":
		m.outcome = WizardAborted
		return m, tea.Quit
	}

	return m, nil
}

// ── Step E keys ───────────────────────────────────────────────────────────────

func (m wizModel) handleStepEKey(msg tea.KeyMsg) (wizModel, tea.Cmd) {
	switch msg.String() {
	case "enter", "g": // "Go to my cycles"
		// Delete onboarding state — onboarding complete.
		_ = onboarding.DeleteState()
		m.outcome = WizardCompleted
		m.view = wizDone
		return m, tea.Quit
	case "q":
		// Back nav blocked on Step E (cycle already created).
		return m, nil
	case "ctrl+c":
		m.outcome = WizardAborted
		return m, tea.Quit
	}
	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m wizModel) View() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}

	switch m.view {
	case wizSplash:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderSplash())
	case wizResume:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderResume())
	case wizWelcome:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderWelcome())
	case wizStepA:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderStepA())
	case wizStepB:
		return m.renderStepB(w, h)
	case wizStepC:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderStepC())
	case wizStepD:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderStepD())
	case wizStepE:
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderStepE())
	}
	return ""
}

// ── Splash ────────────────────────────────────────────────────────────────────

func (m wizModel) renderSplash() string {
	w := m.width
	if w <= 0 {
		w = 80
	}

	var b strings.Builder
	b.WriteString(theme.RenderWordmark(w, version))
	b.WriteString("\n\n")

	tagline := theme.StyleMuted.Render("       Clean cycles,") +
		theme.StyleTeal.Render(" no fuss.")
	b.WriteString(tagline)
	b.WriteString("\n\n")

	b.WriteString("       " + m.sp.View() + " " + theme.StyleSplashStatus.Render("Getting ready…"))
	b.WriteString("\n\n")
	b.WriteString(theme.StyleMuted.Render("       press any key to skip"))

	return b.String()
}

// ── Resume banner ─────────────────────────────────────────────────────────────

func (m wizModel) renderResume() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 4).
		Width(52)

	title := theme.GradientString("WELCOME BACK", theme.Mauve, theme.Lavender, true)
	body := theme.StyleMuted.Render("You were partway through setup.")

	opts := []string{
		renderWizChoice(m.resumeChoice == 0, "Pick up where I left off"),
		renderWizChoice(m.resumeChoice == 1, "Start over"),
	}

	hints := "\n" + theme.StyleMuted.Render("  ↑↓ navigate  enter select")
	return box.Render(title + "\n\n" + body + "\n\n" + strings.Join(opts, "\n") + hints)
}

// ── Welcome ───────────────────────────────────────────────────────────────────

func (m wizModel) renderWelcome() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 4).
		Width(52)

	title := theme.GradientString("RINSE", theme.Mauve, theme.Lavender, true)
	headline := theme.StyleStep.Render("Hey! Let's get your first cycle going.")
	sub := theme.StyleMuted.Render("It only takes a minute to set up.")

	opts := []string{
		renderWizChoice(m.welcomeChoice == 0, "Get started"),
		renderWizChoice(m.welcomeChoice == 1, "Skip setup — take me straight in"),
	}

	hints := "\n" + theme.StyleMuted.Render("  ↑↓ navigate  enter select  q quit")
	return box.Render(title+"\n\n"+headline+"\n"+sub+"\n\n"+strings.Join(opts, "\n")+hints)
}

// ── Step A ────────────────────────────────────────────────────────────────────

func (m wizModel) renderStepA() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 4).
		Width(58)

	progress := renderProgress(1)
	headline := theme.StyleStep.Render("RINSE keeps your cycles on track")

	bullets := []string{
		theme.StyleTeal.Render("  " + theme.IconDot + " ") + theme.StyleMuted.Render("Plan and run wash, dry, and rinse cycles without\n    the mental overhead."),
		theme.StyleTeal.Render("  " + theme.IconDot + " ") + theme.StyleMuted.Render("Set it once. RINSE remembers your preferences\n    and handles the details."),
		theme.StyleTeal.Render("  " + theme.IconDot + " ") + theme.StyleMuted.Render("See everything in one place — history, status,\n    what is next."),
	}

	cta := "\n" + theme.StyleTeal.Render("  → Sounds good, let me try it") +
		"  " + theme.StyleMuted.Render("(enter)")
	skip := theme.StyleMuted.Render("  Skip intro (space)")

	hints := "\n" + theme.StyleMuted.Render("  enter/space continue  q quit")

	return box.Render(progress + "\n\n" + headline + "\n\n" +
		strings.Join(bullets, "\n\n") +
		cta + "\n" + skip + hints)
}

// ── Step B ────────────────────────────────────────────────────────────────────

func (m wizModel) renderStepB(w, h int) string {
	progress := renderProgress(2)
	headline := theme.StyleStep.Render("What would you like to call your first cycle?")
	sub := theme.StyleMuted.Render("You can always rename it later.")

	var b strings.Builder
	b.WriteString(progress + "\n\n")
	b.WriteString(headline + "\n")
	b.WriteString(sub + "\n\n")
	b.WriteString(theme.StyleMuted.Render("  Cycle name") + "\n")
	b.WriteString(m.cycleInput.View() + "\n")

	if m.cycleInputErr != "" {
		b.WriteString("\n" + theme.StyleErr.Render("  " + theme.IconCross + " " + m.cycleInputErr) + "\n")
	}

	// Suggestion chips
	b.WriteString("\n" + theme.StyleMuted.Render("  Suggestions (tab to cycle):"))
	var chips []string
	for i, chip := range cycleChips {
		if m.chipSelected && m.chipIdx == i {
			chips = append(chips, theme.StyleSelected.Render(chip))
		} else {
			chips = append(chips, theme.StyleUnselected.Render(chip))
		}
	}
	b.WriteString("\n  " + strings.Join(chips, "  "))
	b.WriteString("\n\n" + theme.StyleMuted.Render("  enter next  tab chip  esc back  q quit"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 3).
		Width(60)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box.Render(b.String()))
}

// ── Step C ────────────────────────────────────────────────────────────────────

func (m wizModel) renderStepC() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 4).
		Width(58)

	progress := renderProgress(3)
	headline := theme.StyleStep.Render("A few quick settings")
	sub := theme.StyleMuted.Render("We have picked sensible defaults. Change anything you like.")

	toggles := []struct {
		label   string
		desc    string
		val     bool
		focusID int
	}{
		{"Remind me when a cycle is done", "A gentle nudge so nothing sits too long.", m.remindOnComplete, cFocusRemind},
		{"Move to the next step automatically", "Handy if you follow the same routine every time.", m.autoAdvance, cFocusAuto},
		{"Save my cycle history", "See patterns and track what you have run.", m.saveHistory, cFocusHist},
	}

	var rows []string
	for _, t := range toggles {
		cursor := "  "
		if m.cFocus == t.focusID {
			cursor = theme.StyleSelected.Render(theme.IconArrow + " ")
		}
		icon := theme.StyleMuted.Render(theme.IconRadioOff)
		if t.val {
			icon = theme.StyleTeal.Render(theme.IconRadioOn)
		}
		labelStyle := theme.StyleMuted
		if m.cFocus == t.focusID {
			labelStyle = lipgloss.NewStyle().Foreground(theme.Text).Bold(true)
		}
		row := cursor + icon + " " + labelStyle.Render(t.label) + "\n" +
			"    " + theme.StyleMuted.Render(t.desc)
		rows = append(rows, row)
	}

	// Action buttons
	nextCursor := "  "
	if m.cFocus == cFocusNext {
		nextCursor = theme.StyleSelected.Render(theme.IconArrow + " ")
	}
	skipCursor := "  "
	if m.cFocus == cFocusSkip {
		skipCursor = theme.StyleSelected.Render(theme.IconArrow + " ")
	}

	actions := "\n" +
		nextCursor + theme.StyleTeal.Render(theme.IconCheck+" Next") + "\n" +
		skipCursor + theme.StyleMuted.Render("Skip for now")

	hints := "\n" + theme.StyleMuted.Render("  ↑↓ move  space/enter toggle  esc back  q quit")

	return box.Render(progress + "\n\n" + headline + "\n" + sub + "\n\n" +
		strings.Join(rows, "\n\n") + actions + hints)
}

// ── Step D ────────────────────────────────────────────────────────────────────

func (m wizModel) renderStepD() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Mauve).
		Padding(1, 4).
		Width(58)

	progress := renderProgress(4)
	headline := theme.StyleStep.Render("Here is your first cycle")

	// Summary card
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Surface).
		Padding(0, 2).
		Width(46)

	boolStr := func(v bool) string {
		if v {
			return theme.StyleTeal.Render("on")
		}
		return theme.StyleMuted.Render("off")
	}

	summary := fmt.Sprintf("%s  %s\n%s  %s\n%s  %s\n%s  %s",
		theme.StyleKey.Render("Name"),
		theme.StyleVal.Render(m.cycleName),
		theme.StyleKey.Render("Reminders"),
		boolStr(m.remindOnComplete),
		theme.StyleKey.Render("Auto-advance"),
		boolStr(m.autoAdvance),
		theme.StyleKey.Render("History"),
		boolStr(m.saveHistory),
	)

	card := cardStyle.Render(summary)

	var actions string
	if m.creatingCycle {
		actions = "\n" + theme.StyleMuted.Render("  Creating cycle…")
	} else {
		actions = "\n" + theme.StyleTeal.Render("  enter Start cycle") +
			"  " + theme.StyleMuted.Render("a Adjust first")
	}

	var errLine string
	if m.cycleErr != "" {
		errLine = "\n" + theme.StyleErr.Render("  "+theme.IconCross+" "+m.cycleErr) +
			"\n" + theme.StyleMuted.Render("  press enter to try again")
	}

	footer := theme.StyleMuted.Render("  You can edit or delete this cycle any time.")
	hints := "\n" + theme.StyleMuted.Render("  enter start  a adjust  q quit")

	return box.Render(progress + "\n\n" + headline + "\n\n" + card + actions + errLine + "\n" + footer + hints)
}

// ── Step E ────────────────────────────────────────────────────────────────────

func (m wizModel) renderStepE() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Green).
		Padding(1, 4).
		Width(52)

	progress := renderProgress(5)

	// Animation or static
	var celebration string
	if len(m.celebFrames) == 1 || m.celebDone {
		celebration = m.celebFrames[len(m.celebFrames)-1]
	} else {
		idx := m.celebFrame
		if idx >= len(m.celebFrames) {
			idx = len(m.celebFrames) - 1
		}
		celebration = m.celebFrames[idx]
	}

	headline := theme.StyleStep.Render("You are in!")
	body := theme.StyleMuted.Render("Your first cycle is ready. We will let you know when it is done.")

	var subBody string
	if !m.autoAdvance {
		subBody = "\n" + theme.StyleMuted.Render("Hit Start whenever you are ready.")
	}

	cta := "\n" + theme.StyleTeal.Render("  → Go to my cycles") + "  " + theme.StyleMuted.Render("(enter)")
	hints := "\n" + theme.StyleMuted.Render("  enter go to cycles")

	return box.Render(progress + "\n\n" + celebration + "\n\n" + headline + "\n" + body + subBody + cta + hints)
}

// ── Shared render helpers ─────────────────────────────────────────────────────

// renderProgress renders a compact step indicator (1–5 dots).
func renderProgress(current int) string {
	const total = 5
	var dots []string
	for i := 1; i <= total; i++ {
		if i < current {
			dots = append(dots, theme.StyleTeal.Render(theme.IconDot))
		} else if i == current {
			dots = append(dots, theme.StyleSelected.Render(theme.IconDot))
		} else {
			dots = append(dots, theme.StyleMuted.Render(theme.IconCircle))
		}
	}
	return strings.Join(dots, " ")
}

// renderWizChoice renders an option row with selection cursor.
func renderWizChoice(selected bool, label string) string {
	if selected {
		return theme.StyleSelected.Render(theme.IconArrow+" ") + theme.StyleVal.Render(label)
	}
	return "  " + theme.StyleMuted.Render(label)
}

// ── Async helpers ─────────────────────────────────────────────────────────────

// saveStepAsync writes onboarding state in a goroutine (best-effort, non-blocking).
func saveStepAsync(s onboarding.State) {
	go func() {
		if err := onboarding.SaveState(s); err != nil {
			fmt.Fprintf(os.Stderr, "rinse: state write: %v\n", err)
		}
	}()
}

// friendlyCycleErr converts a raw CreateCycle error into a short, user-facing
// message that does not expose Go internals or internal URLs.
func friendlyCycleErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "could not reach rinse backend"):
		return "Could not connect to the RINSE backend. Is it running?"
	case strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "i/o timeout"):
		return "The request timed out. The backend may be slow or unreachable."
	case strings.Contains(msg, "server returned"):
		return "The server rejected the request. Try again or check your settings."
	case strings.Contains(msg, "parse response"):
		return "Got an unexpected response from the server. Try again."
	default:
		return "Something went wrong creating the cycle. Please try again."
	}
}

// ── Entry point ───────────────────────────────────────────────────────────────

// RunOnboardingWizard starts the first-run wizard and returns the outcome.
// Caller should check the outcome to decide how to proceed.
func RunOnboardingWizard(ver string) (WizardOutcome, error) {
	version = ver

	m := newWizModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return WizardAborted, err
	}

	fm, ok := final.(wizModel)
	if !ok {
		return WizardCompleted, nil
	}

	return fm.outcome, nil
}
