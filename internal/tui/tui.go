package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"context"

	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/app"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

var (
	footerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)
)

const assessmentLines = 10

type Model struct {
	app          *app.App
	cards        []*PRCard
	total        int
	fetching     int // PRs whose details are still being fetched
	scoring      int // PRs whose assessments are still in progress
	current      int
	focus        focus
	diffView     DiffView
	chatView     ChatView
	chatActive   bool // true when chat panel is shown instead of diff
	assessmentVP viewport.Model
	spinner      spinner.Model
	modal         commentModal
	actionStatus  string // e.g. "Merging…", "Approving…" — shown in footer while action runs
	program       *tea.Program // for streaming chat sends
	err           error
	width         int
	height        int
}

func New(a *app.App) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return Model{
		app:          a,
		spinner:      s,
		diffView:     NewDiffView(80, 20),
		chatView:     NewChatView(80, 20),
		assessmentVP: viewport.New(80, assessmentLines),
	}
}


func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchPRListCmd(m.app.Repo))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case SetProgramMsg:
		m.program = msg.Program
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeDiffView()
		return m, nil

	case tea.KeyMsg:
		if m.modal.active {
			switch msg.String() {
			case "esc":
				prev := m.modal.prevFocus
				m.modal = commentModal{}
				m.focus = prev
				m.diffView.Focused = (prev == focusDiff)
				m.resizeDiffView()
				return m, nil
			case "enter":
				// Enter sends (same as ctrl+s); alt+enter goes to default/textarea for newline
				body := strings.TrimSpace(m.modal.textarea.Value())
				if body == "" {
					return m, nil
				}
				card := m.currentCard()
				if card == nil {
					return m, nil
				}
				isInline := m.modal.isInline
				filePath := m.modal.filePath
				fileLine := m.modal.fileLine
				commitSHA := m.modal.commitSHA
				prev := m.modal.prevFocus
				rc := github.ReviewComment{
					Author: m.app.CurrentUser,
					Body:   body,
					Path:   filePath,
					Line:   fileLine,
				}
				pendingItem := m.diffView.AddPendingComment(rc)
				m.modal = commentModal{}
				m.focus = prev
				m.diffView.Focused = (prev == focusDiff)
				m.resizeDiffView()
				if isInline {
					return m, postInlineCommentCmd(m.app.Repo, card.PR.Number,
						commitSHA, filePath, fileLine, body, pendingItem)
				}
				return m, postGlobalCommentCmd(m.app.Repo, card.PR.Number, body, pendingItem)
			case "ctrl+s":
				body := strings.TrimSpace(m.modal.textarea.Value())
				if body == "" {
					return m, nil
				}
				card := m.currentCard()
				if card == nil {
					return m, nil
				}
				// Capture modal state before clearing it.
				isInline := m.modal.isInline
				filePath := m.modal.filePath
				fileLine := m.modal.fileLine
				commitSHA := m.modal.commitSHA
				prev := m.modal.prevFocus
				// Add optimistic pending comment immediately.
				rc := github.ReviewComment{
					Author: m.app.CurrentUser,
					Body:   body,
					Path:   filePath,
					Line:   fileLine,
				}
				pendingItem := m.diffView.AddPendingComment(rc)
				// Close modal.
				m.modal = commentModal{}
				m.focus = prev
				m.diffView.Focused = (prev == focusDiff)
				m.resizeDiffView()
				if isInline {
					return m, postInlineCommentCmd(m.app.Repo, card.PR.Number,
						commitSHA, filePath, fileLine, body, pendingItem)
				}
				return m, postGlobalCommentCmd(m.app.Repo, card.PR.Number, body, pendingItem)
			default:
				var cmd tea.Cmd
				m.modal.textarea, cmd = m.modal.textarea.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "q":
			if m.focus == focusChat {
				break // let q go to textarea
			}
			return m, m.cleanupWorktrees()
		case "ctrl+c":
			return m, m.cleanupWorktrees()
		case "?":
			if m.focus == focusChat {
				// Let ? go to the textarea input
				break
			}
			return m, m.activateChat()
		case "tab":
			if m.chatActive {
				// Cycle: assessment -> diff -> chat -> assessment
				switch m.focus {
				case focusAssessment:
					m.focus = focusDiff
					m.diffView.Focused = true
					m.chatView.Focused = false
					m.chatView.Blur()
				case focusDiff:
					m.focus = focusChat
					m.diffView.Focused = false
					m.chatView.Focused = true
					return m, m.chatView.Focus()
				default: // focusChat
					m.focus = focusAssessment
					m.chatView.Focused = false
					m.chatView.Blur()
				}
			} else {
				if m.focus == focusAssessment {
					m.focus = focusDiff
					m.diffView.Focused = true
				} else {
					m.focus = focusAssessment
					m.diffView.Focused = false
				}
			}
			return m, nil
		case "left":
			if m.focus == focusDiff {
				m.diffView.CollapseCurrentFile()
			}
			return m, nil
		case "right":
			if m.focus == focusDiff {
				m.diffView.ExpandCurrentFile()
			}
			return m, nil
		}

		// ctrl+n / ctrl+p navigate PRs from any focus.
		switch msg.String() {
		case "ctrl+n":
			m.navigatePR(1)
			return m, nil
		case "ctrl+p":
			m.navigatePR(-1)
			return m, nil
		}

		// Chat focus: forward keys to chat view
		if m.focus == focusChat {
			switch msg.String() {
			case "esc":
				if m.chatView.streaming {
					// Cancel the running request
					if card := m.currentCard(); card != nil && card.chatCancel != nil {
						card.chatCancel()
						card.chatCancel = nil
					}
					return m, nil
				}
				// Not streaming — close chat
				m.chatActive = false
				m.chatView.Focused = false
				m.chatView.Blur()
				m.focus = focusDiff
				m.diffView.Focused = true
				return m, nil
			case "enter":
				// Enter sends; alt+enter falls through to textarea for newline
				return m, m.sendChatMessage()
			}
			var cmd tea.Cmd
			m.chatView, cmd = m.chatView.Update(msg)
			return m, cmd
		}

		if m.focus == focusAssessment {
			switch msg.String() {
			case "n":
				m.navigatePR(1)
			case "p":
				m.navigatePR(-1)
			case "j", "down":
				if m.assessmentVP.AtBottom() {
					m.focus = focusDiff
					m.diffView.Focused = true
					m.chatView.Focused = false
					m.chatView.Blur()
				} else {
					m.assessmentVP.ScrollDown(1)
				}
			case "k", "up":
				m.assessmentVP.ScrollUp(1)
			case "a":
				if card := m.currentCard(); card != nil && !card.Scoring && !m.isOwnPR(card) {
					m.actionStatus = "Approving…"
					return m, approveCmd(m.app.Repo, card.PR.Number)
				}
			case "m":
				if card := m.currentCard(); card != nil && !card.Scoring && m.isOwnPR(card) {
					m.actionStatus = "Merging…"
					return m, mergeCmd(m.app.Repo, card.PR.Number)
				}
			case "r":
				if card := m.currentCard(); card != nil && !card.Scoring && card.Assessment != nil && !m.isOwnPR(card) {
					m.actionStatus = "Requesting changes…"
					return m, requestChangesCmd(m.app.Repo, card.PR.Number, card.Assessment.ReviewNotes)
				}
			case "s":
				m.navigatePR(1)
			case "c":
				if card := m.currentCard(); card != nil {
					m.openCommentModal(card, false, "", 0)
					return m, m.modal.textarea.Focus()
				}
			}
			return m, nil
		}

		if (msg.String() == "k" || msg.String() == "up") && m.diffView.AtTop() {
			m.focus = focusAssessment
			m.diffView.Focused = false
			return m, nil
		}
		if msg.String() == "c" {
			if card := m.currentCard(); card != nil {
				path, line := m.diffView.CurrentLineTarget()
				m.openCommentModal(card, path != "", path, line)
				return m, m.modal.textarea.Focus()
			}
		}
		var cmd tea.Cmd
		m.diffView, cmd = m.diffView.Update(msg)
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.chatView.streaming || m.chatView.status != "" {
			m.chatView.spinnerView = m.spinner.View()
			m.chatView.rebuildViewport()
		}
		return m, cmd

	case prListFetchedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.total = len(msg.rawPRs)
		m.fetching = len(msg.rawPRs)
		if m.total == 0 {
			return m, nil
		}
		cmds := make([]tea.Cmd, len(msg.rawPRs))
		for i, raw := range msg.rawPRs {
			cmds[i] = fetchPRDetailsCmd(raw, m.app)
		}
		return m, tea.Batch(cmds...)

	case prDetailsFetchedMsg:
		m.fetching--
		if msg.err != nil {
			logger.Error("fetching PR details: %v", msg.err)
			return m, nil
		}
		pr := msg.pr
		card := &PRCard{PR: pr, Scoring: true}
		m.cards = append(m.cards, card)
		m.scoring++

		if len(m.cards) == 1 {
			m.rebuildAssessment()
		}

		return m, tea.Batch(scorePRCmd(pr, m.app), parseDiffCmd(pr))

	case prDiffParsedMsg:
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.parsedFiles = msg.files
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			m.diffView.SetParsedContent(card.parsedFiles, card.PR)
		}
		return m, nil

	case prScoredMsg:
		m.scoring--
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.Scoring = false
				if msg.err != nil {
					card.ScoringErr = msg.err
				} else {
					card.Assessment = msg.assessment
					card.WeightedScore = weightedScore(msg.assessment, m.app)
					card.Verdict = computeVerdict(card.WeightedScore, m.app)
				}
				src := "claude"
				if msg.fromCache {
					src = "cache"
				}
				logger.Info("PR #%d scored via %s: %.1f", msg.prNumber, src, card.WeightedScore)
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			m.rebuildAssessment()
		}
		return m, nil

	case actionDoneMsg:
		m.actionStatus = ""
		if msg.err == nil && m.current < len(m.cards)-1 {
			m.current++
			m.loadCurrentDiff()
			m.rebuildAssessment()
		}
		return m, nil

	case commentSubmittedMsg:
		// Always update the card regardless of which PR is currently on screen.
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				if msg.err == nil {
					rc := github.ReviewComment{
						Author: m.app.CurrentUser,
						Body:   msg.body,
						Path:   msg.filePath,
						Line:   msg.fileLine,
					}
					if msg.isInline {
						card.PR.InlineComments = append(card.PR.InlineComments, rc)
					} else {
						card.PR.Comments = append(card.PR.Comments, rc)
					}
				}
				break
			}
		}
		// Update the live diff view only if we're still looking at that PR.
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
			if msg.err == nil {
				m.diffView.ConfirmComment(msg.pendingItem)
			} else {
				m.diffView.RemoveComment(msg.pendingItem)
			}
		}
		return m, nil

	case chatWorktreeReadyMsg:
		m.chatView.status = ""
		m.chatView.rebuildViewport()
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				if msg.err != nil {
					logger.Error("worktree error for PR #%d: %v", msg.prNumber, msg.err)
				} else {
					card.worktreePath = msg.path
				}
				break
			}
		}
		// If this is the current PR and we were waiting for worktree to send a chat, dispatch it now
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && msg.err == nil {
			if m.chatActive && m.chatView.streaming {
				ctx, cancel := context.WithCancel(context.Background())
				card.chatCancel = cancel
				return m, sendChatCmd(ctx, msg.path, card.PR, card.Assessment, card.chatMessages, card.chatContext, m.program)
			}
		}
		return m, nil

	case chatStatusMsg:
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.status = msg.status
			m.chatView.rebuildViewport()
		}
		return m, nil

	case chatTokenMsg:
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.AppendToken(msg.token)
		}
		return m, nil

	case chatDoneMsg:
		for _, card := range m.cards {
			if card.PR.Number == msg.prNumber {
				card.chatCancel = nil
				if msg.err != nil {
					logger.Error("chat error for PR #%d: %v", msg.prNumber, msg.err)
					card.chatMessages = append(card.chatMessages, chatMessage{
						role:    "assistant",
						content: fmt.Sprintf("Error: %v", msg.err),
					})
				} else if msg.fullResponse != "" {
					card.chatMessages = append(card.chatMessages, chatMessage{
						role:    "assistant",
						content: msg.fullResponse,
					})
				}
				break
			}
		}
		if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && m.chatActive {
			m.chatView.FinishStream(msg.fullResponse)
			m.chatView.SetMessages(card.chatMessages)
		}
		return m, nil
	}

	return m, nil
}

func (m Model) currentCard() *PRCard {
	if m.current < len(m.cards) {
		return m.cards[m.current]
	}
	return nil
}

func (m Model) isOwnPR(card *PRCard) bool {
	return card.PR.Author == m.app.CurrentUser
}

func (m *Model) navigatePR(delta int) {
	next := m.current + delta
	if next < 0 || next >= len(m.cards) {
		return
	}
	m.current = next
	m.assessmentVP.GotoTop()
	m.loadCurrentDiff()
	m.rebuildAssessment()
	// If chat is active, update chat view with new PR's messages
	if m.chatActive {
		if card := m.currentCard(); card != nil {
			m.chatView.SetMessages(card.chatMessages)
			m.chatView.streaming = false
			m.chatView.streamContent = ""
		}
	}
}

func (m *Model) loadCurrentDiff() {
	card := m.currentCard()
	if card == nil || card.PR == nil {
		return
	}
	if card.parsedFiles != nil {
		m.diffView.SetParsedContent(card.parsedFiles, card.PR)
	} else {
		m.diffView.SetContent(card.PR.Diff, card.PR)
	}
}

func (m *Model) resizeDiffView() {
	footerH := 1
	borderH := 1
	diffH := m.height - footerH - assessmentLines - borderH
	if diffH < 4 {
		diffH = 4
	}
	m.diffView.SetSize(m.width, diffH)
	m.chatView.SetSize(m.width, diffH)
	w := m.width
	if w == 0 {
		w = 80
	}
	m.assessmentVP.Width = w - 1                // reserve 1 char for scrollbar
	m.assessmentVP.Height = assessmentLines - 1 // -1 for the panel title bar
	m.rebuildAssessment()
}

func (m *Model) openCommentModal(card *PRCard, isInline bool, path string, line int) {
	ta := textarea.New()
	ta.Placeholder = "Write your comment..."
	ta.SetWidth(m.width - 4)
	ta.SetHeight(4)
	prev := m.focus
	m.modal = commentModal{
		active:    true,
		isInline:  isInline,
		filePath:  path,
		fileLine:  line,
		commitSHA: card.PR.HeadSHA,
		prevFocus: prev,
		textarea:  ta,
	}
	m.focus = focusModal
	m.resizeDiffView()
}

func (m *Model) activateChat() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}

	// Capture diff context from current cursor position
	path, line := m.diffView.CurrentLineTarget()
	if path != "" {
		card.chatContext = &ai.DiffContext{File: path, Line: line}
	} else {
		card.chatContext = nil
	}

	m.chatActive = true
	m.focus = focusChat
	m.diffView.Focused = false
	m.chatView.Focused = true
	m.chatView.SetContext(card.chatContext)
	m.chatView.welcomeText = buildChatWelcome(card, card.chatContext)
	m.chatView.SetMessages(card.chatMessages)
	m.resizeDiffView()

	var cmds []tea.Cmd
	cmds = append(cmds, m.chatView.Focus())

	// Create worktree lazily if not already created
	if card.worktreePath == "" {
		m.chatView.status = "Creating worktree..."
		cmds = append(cmds, createWorktreeCmd(m.app.RepoDir, card.PR.HeadRefName, card.PR.Number))
	}

	return tea.Batch(cmds...)
}

func buildChatWelcome(card *PRCard, ctx *ai.DiffContext) string {
	var sb strings.Builder

	if card.Assessment != nil {
		// Find the highest-scoring factor, or any above 3
		var topName, topReason string
		topScore := 0
		for name, f := range card.Assessment.Factors {
			if f.Score > topScore {
				topScore = f.Score
				topName = name
				topReason = f.Reason
			}
		}
		if topScore >= 3 {
			fmt.Fprintf(&sb, "Highest risk: %s (%d/5) — %s\n\n", topName, topScore, topReason)
		}
	}

	if ctx != nil && ctx.File != "" {
		if ctx.Line > 0 {
			fmt.Fprintf(&sb, "You're looking at %s:%d. ", ctx.File, ctx.Line)
		} else {
			fmt.Fprintf(&sb, "You're looking at %s. ", ctx.File)
		}
	}

	sb.WriteString("What questions do you have?")
	return sb.String()
}

func (m *Model) sendChatMessage() tea.Cmd {
	card := m.currentCard()
	if card == nil {
		return nil
	}
	body := m.chatView.InputValue()
	if body == "" {
		return nil
	}
	if m.chatView.streaming {
		return nil
	}

	m.chatView.ResetInput()
	card.chatMessages = append(card.chatMessages, chatMessage{role: "user", content: body})
	m.chatView.SetMessages(card.chatMessages)
	m.chatView.streaming = true
	m.chatView.status = "Starting Claude..."

	// If worktree is ready, send immediately; otherwise wait for worktreeReady
	if card.worktreePath != "" {
		ctx, cancel := context.WithCancel(context.Background())
		card.chatCancel = cancel
		return sendChatCmd(ctx, card.worktreePath, card.PR, card.Assessment, card.chatMessages, card.chatContext, m.program)
	}
	// Worktree still being created — sendChatCmd will be dispatched when chatWorktreeReadyMsg arrives
	return nil
}

func (m *Model) cleanupWorktrees() tea.Cmd {
	var cmds []tea.Cmd
	for _, card := range m.cards {
		if card.worktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(m.app.RepoDir, card.worktreePath))
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\nError: %v\n\nPress q to quit.\n", m.err)
	}

	if len(m.cards) == 0 {
		if m.fetching == 0 && m.total == 0 {
			return fmt.Sprintf("\n  %s Fetching PRs for %s...\n\n  Press q to quit.\n",
				m.spinner.View(), m.app.Repo)
		}
		if m.fetching > 0 {
			fetched := m.total - m.fetching
			return fmt.Sprintf("\n  %s Loading PRs (%d/%d)...\n\n  Press q to quit.\n",
				m.spinner.View(), fetched, m.total)
		}
		return "\n  No open PRs found.\n\n  Press q to quit.\n"
	}

	width := m.width
	if width == 0 {
		width = 80
	}

	var assessmentTitle string
	if m.focus == focusAssessment {
		var hint string
		if card := m.currentCard(); card != nil && m.isOwnPR(card) {
			hint = " m merge  c comment  s skip  n/p navigate  j/k scroll  tab to diff"
		} else {
			hint = " a approve  r request-changes  c comment  s skip  n/p navigate  j/k scroll  tab to diff"
		}
		assessmentTitle = panelTitleFocused.Render("Assessment") + dimPanelHint(hint, panelTitleFocused, width)
	} else {
		assessmentTitle = panelTitleBlurred.Render("Assessment") + dimPanelHint(" tab to focus", panelTitleBlurred, width)
	}
	assessmentContent := lipgloss.JoinHorizontal(lipgloss.Top, m.assessmentVP.View(), renderScrollbar(m.assessmentVP))
	assessmentPanel := lipgloss.JoinVertical(lipgloss.Left, assessmentTitle, assessmentContent)

	if m.modal.active {
		title := "  Add comment  (Enter submit · Alt+Enter newline · Esc cancel)"
		if m.modal.isInline {
			title = fmt.Sprintf("  Comment on %s:%d  (Enter submit · Alt+Enter newline · Esc cancel)", m.modal.filePath, m.modal.fileLine)
		}
		modalContent := lipgloss.JoinVertical(lipgloss.Left,
			panelTitleFocused.Render(title),
			lipgloss.NewStyle().Padding(0, 1).Render(m.modal.textarea.View()),
		)
		return lipgloss.JoinVertical(lipgloss.Left,
			assessmentPanel,
			m.diffView.ViewWithModal(modalContent),
			m.renderFooter(),
		)
	}

	if m.chatActive {
		// Render tab bar: [Diff] [Chat — file:line]
		showChat := m.focus == focusChat || m.focus == focusAssessment
		tabBar := m.renderTabBar(width, !showChat, showChat)

		var content string
		if showChat {
			content = m.chatView.ViewContent()
		} else {
			content = m.diffView.ViewContent()
		}
		return lipgloss.JoinVertical(lipgloss.Left,
			assessmentPanel,
			tabBar,
			content,
			m.renderFooter(),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		assessmentPanel,
		m.diffView.View(),
		m.renderFooter(),
	)
}

var (
	tabActive = lipgloss.NewStyle().
			Bold(true).
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1)
	tabInactive = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("243")).
			Padding(0, 1)
	tabHint = lipgloss.NewStyle().
		Background(lipgloss.Color("237")).
		Foreground(lipgloss.Color("243")).
		Faint(true)
)

func (m Model) renderTabBar(width int, diffActive, chatActive bool) string {
	var diffTab, chatTab string
	if diffActive {
		diffTab = tabActive.Render("Diff")
	} else {
		diffTab = tabInactive.Render("Diff")
	}

	chatName := m.chatView.ChatTabName()
	if chatActive {
		chatTab = tabActive.Render(chatName)
	} else {
		chatTab = tabInactive.Render(chatName)
	}

	tabs := diffTab + " " + chatTab
	tabsW := lipgloss.Width(tabs)

	// Append hint text for active panel
	var hint string
	if diffActive && m.focus == focusDiff {
		hint = "← collapse  → expand  ] next file  [ prev  c comment  ? chat"
	} else if chatActive && m.focus == focusChat {
		hint = "enter send  alt+enter newline  esc stop/close"
	}

	remaining := width - tabsW
	if remaining > 2 && hint != "" {
		// Right-align hint, pad with background
		hintRendered := tabHint.Render(hint)
		hintW := lipgloss.Width(hintRendered)
		gap := remaining - hintW
		if gap > 0 {
			tabs += tabHint.Render(strings.Repeat(" ", gap)) + hintRendered
		} else {
			tabs += hintRendered
		}
	} else if remaining > 0 {
		tabs += tabHint.Render(strings.Repeat(" ", remaining))
	}

	return tabs
}

func (m Model) renderFooter() string {
	width := m.width
	if width == 0 {
		width = 80
	}
	status := fmt.Sprintf("prx  PR %d/%d", m.current+1, len(m.cards))
	if m.actionStatus != "" {
		status += fmt.Sprintf("  %s %s", m.spinner.View(), m.actionStatus)
	} else if pending := m.fetching + m.scoring; pending > 0 {
		status += fmt.Sprintf("  %s %d loading", m.spinner.View(), pending)
	}
	var hints string
	if m.chatActive && m.focus == focusChat {
		hints = "esc stop/close  |  tab  |  ctrl+n/p nav  |  ctrl+c quit"
	} else if m.chatActive {
		hints = "? chat  |  tab  |  j/k scroll  |  ctrl+n/p nav  |  q quit"
	} else {
		hints = "? chat  |  q quit  |  tab  |  j/k scroll  |  p/n nav  |  ctrl+n/p nav anywhere"
	}
	gap := width - lipgloss.Width(status) - lipgloss.Width(hints) - 2
	if gap < 1 {
		gap = 1
	}
	line := status + strings.Repeat(" ", gap) + hints
	return footerStyle.Width(width).Render(line)
}
