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
// Card lookup helpers
// ---------------------------------------------------------------------------

// findCard finds a card by repo + PR number (composite key for multi-repo).
func (m *Model) findCard(repo string, prNumber int) *PRCard {
	for _, c := range m.cards {
		if c.Ctx.Repo == repo && c.PR.Number == prNumber {
			return c
		}
	}
	return nil
}

// findCardIndex returns the index of a card by repo + PR number, or -1.
func (m *Model) findCardIndex(repo string, prNumber int) int {
	for i, c := range m.cards {
		if c.Ctx.Repo == repo && c.PR.Number == prNumber {
			return i
		}
	}
	return -1
}

// cardKey returns a composite dedup key for a repo + PR number.
type cardKey struct {
	repo     string
	prNumber int
}

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
	// perm.RefreshMsg only carries PR number — find any card with this number.
	for _, card := range m.cards {
		if card.PR.Number == msg.PRNumber {
			return *m, refreshPRCmd(card.PR, card.Ctx)
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
			cmds = append(cmds, scorePRCmd(card.PR, card.Ctx, m.program))
		}
		m.buildScrollback()
	}
	return *m, tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// PR lifecycle handlers
// ---------------------------------------------------------------------------

func (m *Model) handlePRList(msg prListFetchedMsg) (Model, tea.Cmd) {
	m.openListsDone++
	if msg.err != nil {
		m.err = msg.err
		return *m, nil
	}
	existing := make(map[cardKey]bool, len(m.cards))
	for _, card := range m.cards {
		existing[cardKey{card.Ctx.Repo, card.PR.Number}] = true
	}
	var newRaws []map[string]any
	for _, raw := range msg.rawPRs {
		num := int(raw["number"].(float64))
		if !existing[cardKey{msg.ctx.Repo, num}] {
			newRaws = append(newRaws, raw)
		}
	}
	if len(m.cards) == 0 && m.mergedListsDone < len(m.app.Repos) {
		// First open list result — set total but don't finalize yet (merged lists pending).
		m.total = len(msg.rawPRs)
		m.fetching = len(newRaws)
	} else {
		m.fetching += len(newRaws)
	}
	if len(newRaws) == 0 {
		m.checkNoPRs()
		return *m, nil
	}
	m.logStep(fmt.Sprintf("Found %d open PRs in %s", len(msg.rawPRs), msg.ctx.Repo))
	cmds := make([]tea.Cmd, len(newRaws))
	for i, raw := range newRaws {
		cmds[i] = fetchPRDetailsCmd(raw, msg.ctx)
	}
	return *m, tea.Batch(cmds...)
}

func (m *Model) handleMergedPRList(msg mergedPRListFetchedMsg) (Model, tea.Cmd) {
	m.mergedListsDone++
	if msg.err != nil {
		logger.Error("fetching merged PRs: %v", msg.err)
		m.checkNoPRs()
		return *m, nil
	}
	if len(msg.rawPRs) == 0 {
		m.checkNoPRs()
		return *m, nil
	}
	existing := make(map[cardKey]bool, len(m.cards))
	for _, card := range m.cards {
		existing[cardKey{card.Ctx.Repo, card.PR.Number}] = true
	}
	var newRaws []map[string]any
	for _, raw := range msg.rawPRs {
		num := int(raw["number"].(float64))
		if !existing[cardKey{msg.ctx.Repo, num}] {
			newRaws = append(newRaws, raw)
		}
	}
	if len(newRaws) == 0 {
		return *m, nil
	}
	m.total += len(newRaws)
	m.fetching += len(newRaws)
	m.logStep(fmt.Sprintf("Found %d merged PRs in %s", len(newRaws), msg.ctx.Repo))
	cmds := make([]tea.Cmd, len(newRaws))
	for i, raw := range newRaws {
		cmds[i] = fetchPRDetailsCmd(raw, msg.ctx)
	}
	return *m, tea.Batch(cmds...)
}

func (m *Model) handleMergedPRStatus(msg mergedPRStatusMsg) (Model, tea.Cmd) {
	var targetCard *PRCard
	for _, card := range m.cards {
		if card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
			card.MergedStatusChecked = true
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
	// Always score post-merge PRs (results are likely cached anyway).
	if targetCard != nil && !targetCard.Scoring {
		targetCard.Scoring = true
		m.scoring++
		cmds := []tea.Cmd{
			scorePRCmd(targetCard.PR, targetCard.Ctx, m.program),
			createWorktreeCmd(targetCard.Ctx.RepoDir, targetCard.PR.HeadSHA, targetCard.Ctx.Repo, targetCard.PR.Number),
		}
		return *m, tea.Batch(cmds...)
	}
	// If all visible merged PRs are already reviewed and nothing is scoring,
	// we may need to transition out of startup.
	m.tryStartupTransition()
	return *m, nil
}

// markPostMergeReacted marks a post-merge card as reacted and navigates away if hidden.
func (m *Model) markPostMergeReacted(repo string, prNumber int, reaction string) {
	if card := m.findCard(repo, prNumber); card != nil {
		card.UserHasReacted = true
		card.UserReaction = reaction
	}
	if card := m.currentCard(); card != nil && !m.isCardVisible(card) {
		m.skipToVisibleCard()
		m.loadCurrentDiff()
	}
}

// skipToVisibleCard adjusts m.current to the nearest visible card.
// If no visible cards remain, it enters the bulk approve screen (fireworks).
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
	// No visible cards — show bulk approve (fireworks).
	m.tryEnterBulkApprove()
}

func (m *Model) handlePRDetails(msg prDetailsFetchedMsg) (Model, tea.Cmd) {
	m.fetching--
	if msg.err != nil {
		logger.Error("fetching PR details: %v", msg.err)
		return *m, nil
	}
	pr := msg.pr
	ctx := msg.ctx
	if !m.startupDone {
		fetched := m.total - m.fetching
		cached := ""
		key := cache.Key(ctx.Repo, pr.Number, pr.Diff, reviewsText(pr, m.app.CurrentUser), m.app.Config.Criteria)
		if _, ok := m.app.Cache.Get(key); ok {
			cached = " (cached)"
		} else {
			cached = " (needs scoring)"
		}
		m.logStep(fmt.Sprintf("Loaded PR #%d: %s%s (%d/%d)", pr.Number, truncate(pr.Title, 40), cached, fetched, m.total))
	}
	isPostMerge := pr.State == "MERGED"
	card := &PRCard{Ctx: ctx, PR: pr, PostMerge: isPostMerge, Chat: &conversation.ChatSession{
		Status: "Preparing chat...",
	}}
	m.cards = append(m.cards, card)

	m.refreshBulkApproveIfActive()

	if isPostMerge {
		// For merged PRs, defer scoring until we know if user already reviewed.
		// Only parse diff + fetch status for now.
		cmds := []tea.Cmd{parseDiffCmd(ctx.Repo, pr), fetchMergedPRStatusCmd(ctx.Repo, pr.Number, m.app.CurrentUser)}
		cmds = append(cmds, m.fetchBodyImages(pr, ctx.Repo)...)
		if m.fetching == 0 {
			m.tryStartupTransition()
		}
		return *m, tea.Batch(cmds...)
	}

	// Open PRs: score + create worktree immediately.
	card.Scoring = true
	m.scoring++
	cmds := []tea.Cmd{scorePRCmd(pr, ctx, m.program), parseDiffCmd(ctx.Repo, pr)}
	cmds = append(cmds, createWorktreeCmd(ctx.RepoDir, pr.HeadSHA, ctx.Repo, pr.Number))
	cmds = append(cmds, m.fetchBodyImages(pr, ctx.Repo)...)

	// When all details are fetched, check if we can transition — earlier
	// cached scores may have been blocked waiting for fetching to finish.
	if m.fetching == 0 {
		m.tryStartupTransition()
	}

	return *m, tea.Batch(cmds...)
}

func (m *Model) handleDiffParsed(msg prDiffParsedMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		card.parsedFiles = msg.files
		applyHunkAnnotations(card)
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.diffView.SetParsedContent(card.parsedFiles, card.PR)
	}
	return *m, nil
}

func (m *Model) handleScoringToolCall(msg scoringToolCallMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		card.ScoringToolCount = msg.count
		card.ScoringLastTool = msg.lastTool
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber && card.Scoring {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleScoringStatus(msg scoringStatusMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		card.ScoringStatus = msg.status
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber && card.Scoring {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handlePRScored(msg prScoredMsg) (Model, tea.Cmd) {
	m.scoring--
	scoredCard := m.findCard(msg.repo, msg.prNumber)
	if scoredCard != nil {
		scoredCard.Scoring = false
		scoredCard.ScoringToolCount = 0
		scoredCard.ScoringLastTool = ""
		scoredCard.ScoringStatus = ""
		if msg.err != nil {
			scoredCard.ScoringErr = msg.err
		} else {
			scoredCard.Assessment = msg.assessment
			scoredCard.WeightedScore = scoring.WeightedScore(msg.assessment, m.app.Config.Criteria)
			scoredCard.Verdict = scoring.ComputeVerdict(scoredCard.WeightedScore, m.app.Config.Thresholds)
			applyHunkAnnotations(scoredCard)
		}
		src := "claude"
		if msg.fromCache {
			src = "cache"
		}
		logger.Info("PR #%d scored via %s: %.1f", msg.prNumber, src, scoredCard.WeightedScore)
		if !m.startupDone {
			label := fmt.Sprintf("Scored PR #%d: %s (%s, %.1f)", msg.prNumber, truncate(scoredCard.PR.Title, 40), src, scoredCard.WeightedScore)
			m.logStep(label)
		}
	}
	// Transition from startup screen once the card list is stable (all details
	// fetched) and at least one visible PR is scored. Waiting for fetching==0
	// ensures no more card inserts will shift m.current and cause flashing.
	if !m.startupDone && scoredCard != nil && m.fetching == 0 {
		// Count truly visible cards: post-merge cards must have their status checked.
		settled := true
		visibleSettled := 0
		for _, c := range m.cards {
			if c.PostMerge && !c.MergedStatusChecked {
				settled = false
				continue
			}
			if m.isCardVisible(c) {
				visibleSettled++
			}
		}
		allDone := m.scoring == 0 && settled
		hasVisibleScored := false
		for _, c := range m.cards {
			if m.isCardVisible(c) && c.Assessment != nil {
				hasVisibleScored = true
				break
			}
		}

		logger.Info("startup check: PR #%d scored, visibleSettled=%d, hasVisibleScored=%v, allDone=%v, fetching=%d, scoring=%d, settled=%v",
			msg.prNumber, visibleSettled, hasVisibleScored, allDone, m.fetching, m.scoring, settled)

		if hasVisibleScored || allDone {
			m.logDone()
			m.startupDone = true
			m.resizeLayout()
			if visibleSettled == 0 {
				logger.Info("startup: no visible cards, entering bulk approve")
				m.tryEnterBulkApprove()
			} else {
				logger.Info("startup: %d visible cards, showing first", visibleSettled)
				m.skipToVisibleCard()
				m.loadCurrentDiff()
			}
		}
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.buildScrollback()
		if card.parsedFiles != nil {
			m.diffView.SetParsedContent(card.parsedFiles, card.PR)
		}
		// Pre-warm Claude for the current visible PR once assessment is ready.
		m.tryPreWarm(card)
	}
	m.refreshBulkApproveIfActive()
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
	isCurrent := m.currentCard() != nil && m.currentCard().Ctx.Repo == msg.repo && m.currentCard().PR.Number == msg.prNumber
	idx := m.findCardIndex(msg.repo, msg.prNumber)
	if idx >= 0 {
		card := m.cards[idx]
		if msg.activity.State != "" {
			card.PR.State = msg.activity.State
		}
		if msg.activity.MergeStateStatus != "" {
			card.PR.MergeStateStatus = msg.activity.MergeStateStatus
		}
		isDone := (card.PR.State == "MERGED" && !card.PostMerge) || card.PR.State == "CLOSED"
		if isDone && !isCurrent {
			m.cards = append(m.cards[:idx], m.cards[idx+1:]...)
			if m.current > idx {
				m.current--
			}
		} else {
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
			oldReviewsText := reviewsText(card.PR, m.app.CurrentUser)
			card.PR.Checks = msg.activity.Checks
			card.PR.Reviews = msg.activity.Reviews
			card.PR.InlineComments = msg.activity.InlineComments
			card.PR.Comments = msg.activity.Comments
			reviewsChanged := reviewsText(card.PR, m.app.CurrentUser) != oldReviewsText
			if !isDone {
				if shaChanged {
					card.PR.Diff = msg.newDiff
					card.parsedFiles = nil
					card.annotationsApplied = false
					card.Scoring = true
					m.scoring++
					rescoreCmd = forceScorePRCmd(card.PR, card.Ctx, m.program)
				} else {
					card.annotationsApplied = false
					if card.parsedFiles != nil {
						applyHunkAnnotations(card)
					}
					if reviewsChanged {
						card.Scoring = true
						m.scoring++
						rescoreCmd = scorePRCmd(card.PR, card.Ctx, m.program)
					}
				}
			}
		}
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.buildScrollback()
		if shaChanged {
			return *m, tea.Batch(parseDiffCmd(card.Ctx.Repo, card.PR), rescoreCmd)
		}
		if card.parsedFiles != nil {
			m.diffView.SetParsedContent(card.parsedFiles, card.PR)
		}
	}
	return *m, rescoreCmd
}
