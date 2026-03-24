package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/config"
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
	case mergedPRListFetchedMsg:
		return m.handleMergedPRList(msg)
	case mergedPRStatusMsg:
		return m.handleMergedPRStatus(msg)
	case imageFetchedMsg:
		return m.handleImageFetched(msg)
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
	m.openListDone = true
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
	if len(m.cards) == 0 && !m.mergedListDone {
		// First open list result — set total but don't finalize yet (merged list pending).
		m.total = len(msg.rawPRs)
		m.fetching = len(newRaws)
	} else {
		m.fetching += len(newRaws)
	}
	if len(newRaws) == 0 {
		m.checkNoPRs()
		return *m, nil
	}
	m.logStep(fmt.Sprintf("Found %d open PRs, loading details", len(msg.rawPRs)))
	cmds := make([]tea.Cmd, len(newRaws))
	for i, raw := range newRaws {
		cmds[i] = fetchPRDetailsCmd(raw, m.app)
	}
	return *m, tea.Batch(cmds...)
}

func (m *Model) handleMergedPRList(msg mergedPRListFetchedMsg) (Model, tea.Cmd) {
	m.mergedListDone = true
	if msg.err != nil {
		logger.Error("fetching merged PRs: %v", msg.err)
		m.checkNoPRs()
		return *m, nil
	}
	if len(msg.rawPRs) == 0 {
		m.checkNoPRs()
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
	if len(newRaws) == 0 {
		return *m, nil
	}
	m.total += len(newRaws)
	m.fetching += len(newRaws)
	m.logStep(fmt.Sprintf("Found %d merged PRs, loading details", len(newRaws)))
	cmds := make([]tea.Cmd, len(newRaws))
	for i, raw := range newRaws {
		cmds[i] = fetchPRDetailsCmd(raw, m.app)
	}
	return *m, tea.Batch(cmds...)
}

func (m *Model) handleMergedPRStatus(msg mergedPRStatusMsg) (Model, tea.Cmd) {
	var targetCard *PRCard
	for _, card := range m.cards {
		if card.PR.Number == msg.prNumber {
			card.UserHasReviewed = msg.hasReview
			card.UserHasReacted = msg.hasReaction
			targetCard = card
			break
		}
	}
	// If current card just became hidden, navigate to next visible card.
	if card := m.currentCard(); card != nil && !m.isCardVisible(card) {
		m.skipToVisibleCard()
	}
	m.buildScrollback()
	// Only score post-merge PRs that the user hasn't already reviewed.
	if targetCard != nil && !msg.hasReview && !msg.hasReaction && !targetCard.Scoring {
		targetCard.Scoring = true
		m.scoring++
		cmds := []tea.Cmd{
			scorePRCmd(targetCard.PR, m.app, m.program),
			createWorktreeCmd(m.app.RepoDir, targetCard.PR.HeadRefName, targetCard.PR.Number),
		}
		return *m, tea.Batch(cmds...)
	}
	// If all visible merged PRs are already reviewed and nothing is scoring,
	// we may need to transition out of startup.
	m.tryStartupTransition()
	return *m, nil
}

// markPostMergeReacted marks a post-merge card as reacted and navigates away if hidden.
func (m *Model) markPostMergeReacted(prNumber int, reaction string) {
	for _, card := range m.cards {
		if card.PR.Number == prNumber {
			card.UserHasReacted = true
			card.UserReaction = reaction
			break
		}
	}
	if card := m.currentCard(); card != nil && !m.isCardVisible(card) {
		m.skipToVisibleCard()
		m.loadCurrentDiff()
	}
}

// skipToVisibleCard adjusts m.current to the nearest visible card.
func (m *Model) skipToVisibleCard() {
	// Try forward first.
	for i := m.current; i < len(m.cards); i++ {
		if m.isCardVisible(m.cards[i]) {
			m.current = i
			return
		}
	}
	// Then backward.
	for i := m.current - 1; i >= 0; i-- {
		if m.isCardVisible(m.cards[i]) {
			m.current = i
			return
		}
	}
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
	isPostMerge := pr.State == "MERGED"
	card := &PRCard{PR: pr, PostMerge: isPostMerge, Chat: &conversation.ChatSession{
		Status: "Preparing chat...",
	}}
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
	if len(m.cards) == 1 {
		m.buildScrollback()
	}

	if isPostMerge {
		// For merged PRs, defer scoring until we know if user already reviewed.
		// Only parse diff + fetch status for now.
		cmds := []tea.Cmd{parseDiffCmd(pr), fetchMergedPRStatusCmd(m.app.Repo, pr.Number, m.app.CurrentUser)}
		cmds = append(cmds, m.fetchBodyImages(pr)...)
		return *m, tea.Batch(cmds...)
	}

	// Open PRs: score + create worktree immediately.
	card.Scoring = true
	m.scoring++
	cmds := []tea.Cmd{scorePRCmd(pr, m.app, m.program), parseDiffCmd(pr)}
	cmds = append(cmds, createWorktreeCmd(m.app.RepoDir, pr.HeadRefName, pr.Number))
	cmds = append(cmds, m.fetchBodyImages(pr)...)
	return *m, tea.Batch(cmds...)
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
		// Pre-warm Claude for the current visible PR once assessment is ready.
		m.tryPreWarm(card)
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
		isDone := (card.PR.State == "MERGED" && !card.PostMerge) || card.PR.State == "CLOSED"
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
