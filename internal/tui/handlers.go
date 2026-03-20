package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/tui/conversation"
	"github.com/sleuth-io/prx/internal/tui/perm"
	"github.com/sleuth-io/prx/internal/tui/scoring"
)

// ---------------------------------------------------------------------------
// Scene update dispatchers
// ---------------------------------------------------------------------------

func (m *Model) updateReview(msg tea.Msg) (Model, tea.Cmd) {
	// PR lifecycle and chat messages handled at Model level (mutate shared state)
	switch msg := msg.(type) {
	case prListFetchedMsg:
		return m.handlePRList(msg)
	case prDetailsFetchedMsg:
		return m.handlePRDetails(msg)
	case prDiffParsedMsg:
		return m.handleDiffParsed(msg)
	case scoringToolCallMsg:
		return m.handleScoringToolCall(msg)
	case scoringStatusMsg:
		return m.handleScoringStatus(msg)
	case prScoredMsg:
		return m.handlePRScored(msg)
	case prRefreshedMsg:
		return m.handlePRRefreshed(msg)
	case commentSubmittedMsg:
		return m.handleCommentSubmitted(msg)
	case chatWorktreeReadyMsg:
		return m.handleChatWorktreeReady(msg)
	case chatStatusMsg:
		return m.handleChatStatus(msg)
	case chatToolCallMsg:
		return m.handleChatToolCall(msg)
	case chatTokenMsg:
		return m.handleChatToken(msg)
	case chatDoneMsg:
		return m.handleChatDone(msg)
	}

	// Everything else delegated to the active scene
	var cmd tea.Cmd
	m.scene, cmd = m.scene.Update(msg, m)
	return *m, cmd
}

// ---------------------------------------------------------------------------
// Global message handlers
// ---------------------------------------------------------------------------

func (m *Model) handleSetProgram(msg SetProgramMsg) (Model, tea.Cmd) {
	m.program = msg.Program
	socketPath, cleanup, err := perm.Listen(msg.Program)
	if err != nil {
		logger.Error("perm socket: %v", err)
	} else {
		m.permSocketPath = socketPath
		m.permCleanup = cleanup
	}
	return *m, nil
}

func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.resizeLayout()
	m.buildScrollback()
	return *m, nil
}

func (m *Model) handleSpinnerTick(msg spinner.TickMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	// Rebuild scrollback if anything is actively streaming or scoring
	if card := m.currentCard(); card != nil {
		chatActive := card.Chat != nil && (card.Chat.Streaming || card.Chat.Status != "")
		if chatActive || card.Scoring {
			m.buildScrollback()
		}
	}
	// Pass spinner to bulk approve scene if active
	if ba, ok := m.scene.(*BulkApproveScene); ok {
		ba.model.SetSpinnerView(m.spinner.View())
	}
	return *m, cmd
}

func (m *Model) handlePermRefresh(msg perm.RefreshMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.PRNumber {
			return *m, refreshPRCmd(card.PR, m.app)
		}
	}
	return *m, nil
}

func (m *Model) handleConfigReload(_ perm.ConfigReloadMsg) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	oldHash := config.CriteriaHash(m.app.Config.Criteria)
	if cfg, err := config.Load(); err == nil {
		m.app.Config = cfg
	}
	if config.CriteriaHash(m.app.Config.Criteria) != oldHash {
		for _, card := range m.cards {
			card.Scoring = true
			m.scoring++
			cmds = append(cmds, scorePRCmd(card.PR, m.app, m.program))
		}
		m.buildScrollback()
	}
	return *m, tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// PR lifecycle handlers
// ---------------------------------------------------------------------------

func (m *Model) handlePRList(msg prListFetchedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err
		return *m, nil
	}
	existing := make(map[int]bool, len(m.cards))
	for _, card := range m.cards {
		existing[card.PR.Number] = true
	}
	var newRaws []map[string]any
	for _, raw := range msg.rawPRs {
		if num := int(raw["number"].(float64)); !existing[num] {
			newRaws = append(newRaws, raw)
		}
	}
	if len(m.cards) == 0 {
		m.total = len(msg.rawPRs)
		m.fetching = len(newRaws)
	} else {
		m.fetching += len(newRaws)
	}
	if len(newRaws) == 0 {
		m.logDone()
		if len(msg.rawPRs) == 0 {
			m.startupLog = append(m.startupLog, startupEntry{text: "No open PRs found", done: true})
			m.noPRs = true
		}
		return *m, nil
	}
	m.logStep(fmt.Sprintf("Found %d open PRs, loading details", len(msg.rawPRs)))
	cmds := make([]tea.Cmd, len(newRaws))
	for i, raw := range newRaws {
		cmds[i] = fetchPRDetailsCmd(raw, m.app)
	}
	return *m, tea.Batch(cmds...)
}

func (m *Model) handlePRDetails(msg prDetailsFetchedMsg) (Model, tea.Cmd) {
	m.fetching--
	if msg.err != nil {
		logger.Error("fetching PR details: %v", msg.err)
		return *m, nil
	}
	pr := msg.pr
	if !m.startupDone {
		fetched := m.total - m.fetching
		cached := ""
		key := cache.Key(m.app.Repo, pr.Number, pr.Diff, reviewsText(pr), m.app.Config.Criteria)
		if _, ok := m.app.Cache.Get(key); ok {
			cached = " (cached)"
		} else {
			cached = " (needs scoring)"
		}
		m.logStep(fmt.Sprintf("Loaded PR #%d: %s%s (%d/%d)", pr.Number, truncate(pr.Title, 40), cached, fetched, m.total))
	}
	card := &PRCard{PR: pr, Scoring: true, Chat: &conversation.ChatSession{}}
	// Insert sorted by PR number descending (newest first).
	idx := 0
	for idx < len(m.cards) && m.cards[idx].PR.Number > pr.Number {
		idx++
	}
	m.cards = append(m.cards, nil)
	copy(m.cards[idx+1:], m.cards[idx:])
	m.cards[idx] = card
	if m.fetching == 0 && idx <= m.current && len(m.cards) > 1 {
		m.current++
	}
	m.scoring++
	if len(m.cards) == 1 {
		m.buildScrollback()
	}
	return *m, tea.Batch(scorePRCmd(pr, m.app, m.program), parseDiffCmd(pr))
}

func (m *Model) handleDiffParsed(msg prDiffParsedMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.parsedFiles = msg.files
			applyHunkAnnotations(card)
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.diffView.SetParsedContent(card.parsedFiles, card.PR)
	}
	return *m, nil
}

func (m *Model) handleScoringToolCall(msg scoringToolCallMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.ScoringToolCount = msg.count
			card.ScoringLastTool = msg.lastTool
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && card.Scoring {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleScoringStatus(msg scoringStatusMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.ScoringStatus = msg.status
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && card.Scoring {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handlePRScored(msg prScoredMsg) (Model, tea.Cmd) {
	m.scoring--
	var scoredCard *PRCard
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			scoredCard = card
			card.Scoring = false
			card.ScoringToolCount = 0
			card.ScoringLastTool = ""
			card.ScoringStatus = ""
			if msg.err != nil {
				card.ScoringErr = msg.err
			} else {
				card.Assessment = msg.assessment
				card.WeightedScore = scoring.WeightedScore(msg.assessment, m.app.Config.Criteria)
				card.Verdict = scoring.ComputeVerdict(card.WeightedScore, m.app.Config.Thresholds)
				applyHunkAnnotations(card)
			}
			src := "claude"
			if msg.fromCache {
				src = "cache"
			}
			logger.Info("PR #%d scored via %s: %.1f", msg.prNumber, src, card.WeightedScore)
			if !m.startupDone {
				label := fmt.Sprintf("Scored PR #%d: %s (%s, %.1f)", msg.prNumber, truncate(card.PR.Title, 40), src, card.WeightedScore)
				m.logStep(label)
			}
			break
		}
	}
	// Transition from startup screen once the first PR is scored (or errored).
	if !m.startupDone && scoredCard != nil && !scoredCard.Scoring {
		m.logDone()
		m.startupDone = true
		m.loadCurrentDiff()
		m.resizeLayout()
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.buildScrollback()
		if card.parsedFiles != nil {
			m.diffView.SetParsedContent(card.parsedFiles, card.PR)
		}
	}
	if m.scoring == 0 && m.fetching == 0 && !m.bulkApproveShown {
		m.tryEnterBulkApprove()
	}
	return *m, nil
}

func (m *Model) handlePRRefreshed(msg prRefreshedMsg) (Model, tea.Cmd) {
	if cs, ok := m.scene.(*ConversationScene); ok && !cs.actionDone {
		cs.actionStatus = ""
	}
	if msg.err != nil {
		logger.Error("refresh PR: %v", msg.err)
		return *m, nil
	}
	var rescoreCmd tea.Cmd
	shaChanged := msg.newDiff != ""
	isCurrent := m.currentCard() != nil && m.currentCard().PR.Number == msg.prNumber
	for i, card := range m.cards {
		if card.PR.Number != msg.prNumber {
			continue
		}
		if msg.activity.State != "" {
			card.PR.State = msg.activity.State
		}
		if msg.activity.MergeStateStatus != "" {
			card.PR.MergeStateStatus = msg.activity.MergeStateStatus
		}
		isDone := card.PR.State == "MERGED" || card.PR.State == "CLOSED"
		if isDone && !isCurrent {
			m.cards = append(m.cards[:i], m.cards[i+1:]...)
			if m.current > i {
				m.current--
			}
			break
		}
		if msg.activity.Title != "" {
			card.PR.Title = msg.activity.Title
		}
		if msg.activity.Body != "" {
			card.PR.Body = msg.activity.Body
		}
		if msg.activity.HeadSHA != "" {
			card.PR.HeadSHA = msg.activity.HeadSHA
			card.PR.HeadRefName = msg.activity.HeadRefName
		}
		oldReviewsText := reviewsText(card.PR)
		card.PR.Checks = msg.activity.Checks
		card.PR.Reviews = msg.activity.Reviews
		card.PR.InlineComments = msg.activity.InlineComments
		card.PR.Comments = msg.activity.Comments
		reviewsChanged := reviewsText(card.PR) != oldReviewsText
		if !isDone {
			if shaChanged {
				card.PR.Diff = msg.newDiff
				card.parsedFiles = nil
				card.annotationsApplied = false
				card.Scoring = true
				m.scoring++
				rescoreCmd = forceScorePRCmd(card.PR, m.app, m.program)
			} else {
				card.annotationsApplied = false
				if card.parsedFiles != nil {
					applyHunkAnnotations(card)
				}
				if reviewsChanged {
					card.Scoring = true
					m.scoring++
					rescoreCmd = scorePRCmd(card.PR, m.app, m.program)
				}
			}
		}
		break
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.buildScrollback()
		if shaChanged {
			return *m, tea.Batch(parseDiffCmd(card.PR), rescoreCmd)
		}
		if card.parsedFiles != nil {
			m.diffView.SetParsedContent(card.parsedFiles, card.PR)
		}
	}
	return *m, rescoreCmd
}

func (m *Model) handleCommentSubmitted(msg commentSubmittedMsg) (Model, tea.Cmd) {
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
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		if msg.err == nil {
			m.diffView.ConfirmComment(msg.pendingItem)
		} else {
			m.diffView.RemoveComment(msg.pendingItem)
		}
	}
	return *m, nil
}

// ---------------------------------------------------------------------------
// Chat handlers
// ---------------------------------------------------------------------------

func (m *Model) handleChatWorktreeReady(msg chatWorktreeReadyMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			if msg.err != nil {
				logger.Error("worktree error for PR #%d: %v", msg.prNumber, msg.err)
			}
			card.Chat.HandleWorktreeReady(msg.path, msg.err)
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber && msg.err == nil {
		if card.Chat.IsStreaming() {
			card.Chat.Status = ""
			ctx, cancel := context.WithCancel(context.Background())
			card.Chat.Cancel = cancel
			m.buildScrollback()
			return *m, sendChatCmd(ctx, msg.path, card.PR, card.Assessment, card.Chat.Messages,
				nil, m.app.Config.Review.Model, m.app.Repo, m.isOwnPR(card),
				m.permSocketPath, m.program, m.skillCatalog())
		}
	}
	m.buildScrollback()
	return *m, nil
}

func (m *Model) handleChatStatus(msg chatStatusMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.Chat.HandleStatus(msg.status)
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleChatToolCall(msg chatToolCallMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.Chat.HandleToolCall(msg.count, msg.lastTool)
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleChatToken(msg chatTokenMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.Chat.HandleToken(msg.token)
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleChatDone(msg chatDoneMsg) (Model, tea.Cmd) {
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			if msg.err != nil {
				logger.Error("chat error for PR #%d: %v", msg.prNumber, msg.err)
			}
			card.Chat.HandleDone(msg.fullResponse, msg.err)
			break
		}
	}
	if card := m.currentCard(); card != nil && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}
