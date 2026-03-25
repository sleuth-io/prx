package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/imgrender"
	"github.com/sleuth-io/prx/internal/logger"
)

// ---------------------------------------------------------------------------
// Chat handlers
// ---------------------------------------------------------------------------

func (m *Model) handleChatWorktreeReady(msg chatWorktreeReadyMsg) (Model, tea.Cmd) {
	logger.Info("chat: worktree ready for PR #%d (err=%v)", msg.prNumber, msg.err)
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		if msg.err != nil {
			logger.Error("worktree error for PR #%d: %v", msg.prNumber, msg.err)
		}
		card.Chat.HandleWorktreeReady(msg.path, msg.err)
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber && msg.err == nil {
		if card.Chat.IsStreaming() {
			logger.Info("chat: worktree ready, starting sendChatCmd")
			card.Chat.Status = ""
			ctx, cancel := context.WithCancel(context.Background())
			card.Chat.Cancel = cancel
			m.buildScrollback()
			return *m, sendChatCmd(ctx, msg.path, card.PR, card.Assessment, card.Chat.Messages,
				nil, m.app.Config.Review.Model, card.Ctx.Repo, m.isOwnPR(card),
				m.permSocketPath, m.program, m.skillCatalog())
		}
		// Try pre-warming if worktree just became available for the current PR.
		m.tryPreWarm(card)
	}
	m.buildScrollback()
	return *m, nil
}

func (m *Model) handleChatStatus(msg chatStatusMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		card.Chat.HandleStatus(msg.status)
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleChatToolCall(msg chatToolCallMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		card.Chat.HandleToolCall(msg.count, msg.lastTool)
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleChatToken(msg chatTokenMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		card.Chat.HandleToken(msg.token)
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

func (m *Model) handleChatDone(msg chatDoneMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
		if msg.err != nil {
			logger.Error("chat error for PR #%d: %v", msg.prNumber, msg.err)
		}
		card.Chat.HandleDone(msg.fullResponse, msg.err)
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		m.buildScrollback()
	}
	return *m, nil
}

// ---------------------------------------------------------------------------
// Comment & image handlers
// ---------------------------------------------------------------------------

func (m *Model) handleCommentSubmitted(msg commentSubmittedMsg) (Model, tea.Cmd) {
	if card := m.findCard(msg.repo, msg.prNumber); card != nil {
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
	}
	if card := m.currentCard(); card != nil && card.Ctx.Repo == msg.repo && card.PR.Number == msg.prNumber {
		if msg.err == nil {
			m.diffView.ConfirmComment(msg.pendingItem)
		} else {
			m.diffView.RemoveComment(msg.pendingItem)
		}
	}
	return *m, nil
}

// fetchBodyImages returns commands to fetch any images in the PR body.
func (m *Model) fetchBodyImages(pr *github.PR, repo string) []tea.Cmd {
	if m.imageCache == nil || pr.Body == "" {
		return nil
	}
	refs := imgrender.ExtractImages(pr.Body)
	var cmds []tea.Cmd
	for _, ref := range refs {
		if m.imageCache.Get(ref.URL) == "" {
			cmds = append(cmds, fetchImageCmd(repo, pr.Number, ref.URL, m.imageCache))
		}
	}
	return cmds
}

func (m *Model) handleImageFetched(msg imageFetchedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		logger.Error("image fetch failed for PR #%d: %v", msg.prNumber, msg.err)
	} else {
		logger.Info("image fetched for PR #%d: %s", msg.prNumber, msg.url)
	}
	// Rebuild scrollback so the image appears
	m.buildScrollback()
	return *m, nil
}
