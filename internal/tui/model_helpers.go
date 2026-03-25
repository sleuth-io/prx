package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/mcp"
	"github.com/sleuth-io/prx/internal/tui/bulkapprove"
	"github.com/sleuth-io/prx/internal/tui/scoring"
)

// ---------------------------------------------------------------------------
// Model helpers
// ---------------------------------------------------------------------------

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func (m Model) currentCard() *PRCard {
	if m.current < len(m.cards) {
		return m.cards[m.current]
	}
	return nil
}

// tryStartupTransition checks if we can exit the startup screen.
// Called when a merged PR status arrives and no scoring was triggered.
func (m *Model) tryStartupTransition() {
	if m.startupDone || m.fetching > 0 {
		return
	}
	// Check if any visible card exists (scored or not — unscored merged PRs are still viewable).
	hasVisible := false
	for _, card := range m.cards {
		if m.isCardVisible(card) {
			hasVisible = true
			break
		}
	}
	if !hasVisible {
		if len(m.cards) > 0 {
			// Cards exist but all are reviewed — show bulk approve (fireworks).
			logger.Info("tryStartupTransition: %d cards but none visible, entering bulk approve", len(m.cards))
			m.logDone()
			m.startupDone = true
			m.resizeLayout()
			m.tryEnterBulkApprove()
			return
		}
		m.checkNoPRs()
		return
	}
	// If there's at least one visible card and nothing is being fetched, transition.
	// Scoring may still be in progress but the user can start browsing.
	logger.Info("tryStartupTransition: %d visible cards, transitioning", m.visibleCardCount())
	m.logDone()
	m.startupDone = true
	m.skipToVisibleCard()
	m.loadCurrentDiff()
	m.resizeLayout()
}

// checkNoPRs sets noPRs=true only when all repo lists have returned and there are no cards or fetches pending.
func (m *Model) checkNoPRs() {
	if m.openListsDone < len(m.app.Repos) || m.mergedListsDone < len(m.app.Repos) {
		return
	}
	if len(m.cards) == 0 && m.fetching == 0 {
		m.logDone()
		m.startupLog = append(m.startupLog, startupEntry{text: "No PRs to review", done: true})
		m.noPRs = true
	}
}

// isCardVisible returns whether a card should be shown in the current view.
// Open PRs are always visible. Post-merge PRs are hidden when already reviewed/reacted
// unless showAllMerged is toggled on.
func (m Model) isCardVisible(card *PRCard) bool {
	if !card.PostMerge {
		return true
	}
	if m.showAllMerged {
		return true
	}
	return !card.UserHasReviewed && !card.UserHasReacted
}

// visibleCardCount returns the number of currently visible cards.
func (m Model) visibleCardCount() int {
	n := 0
	for _, card := range m.cards {
		if m.isCardVisible(card) {
			n++
		}
	}
	return n
}

func (m Model) isOwnPR(card *PRCard) bool {
	return card.PR.Author == m.app.CurrentUser
}

// multiRepo returns true when reviewing PRs across multiple repositories.
func (m Model) multiRepo() bool {
	return len(m.app.Repos) > 1
}

func (m *Model) navigatePR(delta int, s *ConversationScene) {
	next := m.current + delta
	for next >= 0 && next < len(m.cards) {
		if m.isCardVisible(m.cards[next]) {
			break
		}
		next += delta
	}
	if next < 0 || next >= len(m.cards) {
		return
	}
	m.current = next
	s.actionStatus = ""
	s.actionDone = false
	m.loadCurrentDiff()
	s.BuildScrollback(m)
	// Pre-warm Claude for the newly visible PR.
	if card := m.currentCard(); card != nil {
		m.tryPreWarm(card)
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

func (m *Model) buildRenderData(card *PRCard) scoring.RenderData {
	return scoring.RenderData{
		Repo:             card.Ctx.Repo,
		MultiRepo:        m.multiRepo(),
		PR:               card.PR,
		Assessment:       card.Assessment,
		Score:            card.WeightedScore,
		Verdict:          card.Verdict,
		Scoring:          card.Scoring,
		ScoringErr:       card.ScoringErr,
		SpinnerView:      m.spinner.View(),
		Criteria:         m.app.Config.Criteria,
		ScoringToolCount: card.ScoringToolCount,
		ScoringLastTool:  card.ScoringLastTool,
		ScoringStatus:    card.ScoringStatus,
		ParsedFiles:      card.parsedFiles,
		ImageCache:       m.imageCache,
		PostMerge:        card.PostMerge,
		UserReaction:     card.UserReaction,
	}
}

// startChatCmd creates a sendChatCmd for the given card.
// If a warm process is available, it uses that instead of spawning a new one.
func (m *Model) startChatCmd(card *PRCard) tea.Cmd {
	wp := card.Chat.TakeWarm()
	if wp != nil {
		logger.Info("chat: using warm process for PR #%d", card.PR.Number)
		card.Chat.Cancel = wp.Kill
		return sendChatCmdWarm(wp, card.PR, card.Ctx.Repo, card.Assessment, card.Chat.Messages,
			nil, m.isOwnPR(card), m.permSocketPath, m.program, m.skillCatalog())
	}
	ctx, cancel := context.WithCancel(context.Background())
	card.Chat.Cancel = cancel
	return sendChatCmd(ctx, card.Chat.WorktreePath, card.PR, card.Assessment, card.Chat.Messages,
		nil, m.app.Config.Review.Model, card.Ctx.Repo, m.isOwnPR(card),
		m.permSocketPath, m.program, m.skillCatalog())
}

// buildClaudeArgs builds the common Claude CLI arguments for a card (tools, MCP config, model)
// but does NOT include -p/prompt or output format flags.
// Returns (args, mcpConfigFile) — caller must clean up the config file.
func (m *Model) buildClaudeArgs(card *PRCard) []string {
	actionTools := ActionToolNames(m.isOwnPR(card))

	allTools := append([]string{"Read", "Glob", "Grep"}, mcp.ToolNames()...)
	var availableActions []string
	if m.permSocketPath != "" {
		availableActions = actionTools
		allTools = append(allTools, availableActions...)
	}
	allowedTools := strings.Join(allTools, ",")

	args := []string{
		"--verbose",
		"--allowedTools", allowedTools,
		"--strict-mcp-config",
		"--no-session-persistence",
	}

	// MCP config file for the prx mcp-server.
	binPath, binErr := os.Executable()
	if binErr == nil && m.permSocketPath != "" {
		mcpCfg := map[string]any{
			"mcpServers": map[string]any{
				"prx": map[string]any{
					"command": binPath,
					"args": []string{
						"mcp-server",
						"--socket=" + m.permSocketPath,
						"--repo=" + card.Ctx.Repo,
						"--pr=" + strconv.Itoa(card.PR.Number),
						"--commit=" + card.PR.HeadSHA,
					},
				},
			},
		}
		if cfgBytes, err := json.Marshal(mcpCfg); err == nil {
			if tmp, err := os.CreateTemp("", "prx-mcp-*.json"); err == nil {
				_, _ = tmp.Write(cfgBytes)
				_ = tmp.Close()
				args = append(args, "--mcp-config", tmp.Name())
			}
		}
	}

	if m.app.Config.Review.Model != "" {
		args = append(args, "--model", m.app.Config.Review.Model)
	}

	return args
}

// tryPreWarm starts a Claude CLI process for the card that initializes hooks
// and MCP servers but does not send a prompt (zero tokens consumed).
// When the user sends a message, sendChatCmd will use this warm process.
func (m *Model) tryPreWarm(card *PRCard) {
	if card.Chat.HasWarm() || card.Chat.WorktreePath == "" || card.Assessment == nil {
		return
	}

	logger.Info("chat: pre-warming Claude for PR #%d", card.PR.Number)
	card.Chat.Status = "Starting Claude..."

	repo := card.Ctx.Repo
	prNumber := card.PR.Number
	args := m.buildClaudeArgs(card)
	wp := ai.StartWarm(context.Background(), args, card.Chat.WorktreePath)
	wp.OnStatus = func(status string) {
		m.program.Send(chatStatusMsg{repo: repo, prNumber: prNumber, status: status})
	}
	card.Chat.Warm = wp

	// Monitor warm process readiness in background to clear status.
	go func() {
		<-wp.Ready()
		if wp.InitErr() != nil {
			logger.Error("chat: warm process init failed for PR #%d: %v", prNumber, wp.InitErr())
		} else {
			logger.Info("chat: warm process ready for PR #%d", prNumber)
		}
		m.program.Send(chatStatusMsg{repo: repo, prNumber: prNumber, status: ""})
	}()
}

// hardReset cancels all in-flight work, clears cache, and restarts from scratch.
func (m *Model) hardReset() tea.Cmd {
	var cmds []tea.Cmd
	for _, card := range m.cards {
		card.Chat.Cleanup()
		if card.Chat.WorktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(card.Ctx.RepoDir, card.Chat.WorktreePath))
		}
	}
	m.convScene.actionStatus = ""
	m.convScene.actionDone = false
	m.scene = m.convScene
	m.app.Cache.Clear()
	m.cards = nil
	m.total = 0
	m.fetching = 0
	m.scoring = 0
	m.current = 0
	m.startupDone = false
	m.openListsDone = 0
	m.mergedListsDone = 0
	log := []startupEntry{
		{text: fmt.Sprintf("Signed in as %s", m.app.CurrentUser), done: true},
	}
	for _, r := range m.app.Repos {
		log = append(log, startupEntry{text: fmt.Sprintf("Fetching PRs from %s", r.Repo)})
	}
	m.startupLog = log
	cmds = append(cmds, m.spinner.Tick)
	for _, r := range m.app.Repos {
		cmds = append(cmds, fetchPRListCmd(r), fetchMergedPRListCmd(r))
	}
	return tea.Batch(cmds...)
}

func (m *Model) cleanupWorktrees() tea.Cmd {
	if m.permCleanup != nil {
		m.permCleanup()
		m.permCleanup = nil
	}
	var cmds []tea.Cmd
	for _, card := range m.cards {
		card.Chat.Cleanup()
		if card.Chat.WorktreePath != "" {
			cmds = append(cmds, removeWorktreeCmd(card.Ctx.RepoDir, card.Chat.WorktreePath))
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
}

// buildScrollback is a convenience that delegates to the conversation scene.
func (m *Model) buildScrollback() {
	m.convScene.BuildScrollback(m)
}

// tryEnterBulkApprove creates a bulk approve scene if there are eligible PRs.
func (m *Model) tryEnterBulkApprove() bool {
	var items []bulkapprove.Item
	for _, card := range m.cards {
		if !m.isCardVisible(card) || m.isOwnPR(card) {
			continue
		}
		if card.PostMerge {
			// Post-merge cards don't need scoring to be eligible
			if card.Scoring {
				continue
			}
			summary := ""
			if card.Assessment != nil {
				summary = card.Assessment.RiskSummary
			}
			items = append(items, bulkapprove.ItemFromCard(card.Ctx.Repo, card.PR, card.WeightedScore, card.Verdict, summary, true))
		} else if !card.Scoring && card.ScoringErr == nil {
			summary := ""
			if card.Assessment != nil {
				summary = card.Assessment.RiskSummary
			}
			items = append(items, bulkapprove.ItemFromCard(card.Ctx.Repo, card.PR, card.WeightedScore, card.Verdict, summary, false))
		}
	}
	ba := bulkapprove.New(m.app.CurrentUser, items, m.width, m.height)
	m.bulkApproveShown = true
	m.scene = newBulkApproveScene(ba, m.convScene, m.width, m.height)
	return true
}

// refreshBulkApproveIfActive rebuilds the bulk approve scene in-place
// when card state changes (e.g. new PRs fetched, scoring complete).
func (m *Model) refreshBulkApproveIfActive() {
	if _, ok := m.scene.(*BulkApproveScene); ok {
		m.tryEnterBulkApprove()
	}
}
