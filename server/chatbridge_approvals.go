package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
)

// Remote approvals of the chat bridges (issue #5, part 3). A pending
// interception request of a task is pushed to every chat that selected the
// task, with Allow and Block buttons. A press goes through the same
// Interceptor.Decide as the web UI's buttons, with the chat user recorded as
// the approver.
//
// The buttons carry a one-time nonce: 16 random bytes, of which only the
// SHA-256 is stored (chat_bridge_approvals). A press counts once, from the
// chat and message the buttons were sent to, before they expire, and only
// for a sender the Router let through (an allowed chat, and in a group an
// allowed user). The nonce is spent in the same statement that checks it, so
// two presses, or a press and the request being decided in ARTEX, cannot
// both act. Buttons whose request was decided elsewhere, or that expired, are
// removed from the message by the same loop that pushes.

const (
	// chatBridgeApprovalTTL is how long the buttons of a push work.
	chatBridgeApprovalTTL = 30 * time.Minute
	// chatBridgeApprovalTick is how often pending requests are looked for.
	chatBridgeApprovalTick = 3 * time.Second
	// chatBridgeApprovalBatch caps the pushes and closes of one tick.
	chatBridgeApprovalBatch = 20
	// chatBridgeApprovalExcerpt caps the request excerpt (share_text only).
	chatBridgeApprovalExcerpt = 300
	// callback data: "ap:<nonce>:<a|d>"
	approvalDataPrefix = "ap"
)

// runChatBridgeApprovals pushes and closes approval messages until ctx ends.
func (s *Server) runChatBridgeApprovals(ctx context.Context) {
	t := time.NewTicker(chatBridgeApprovalTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, kind := range chatbridge.Kinds() {
			s.chatBridgeApprovalTick(ctx, kind)
		}
	}
}

// chatBridgeApprovalTick runs one round for a bridge: close what stopped
// being actionable, then push what is new.
func (s *Server) chatBridgeApprovalTick(ctx context.Context, kind string) {
	tr, cfg, b, err := s.chatBridges.openBridge(ctx, kind)
	if err != nil {
		return // off, not configured, or unreadable: the poller reports it
	}
	s.closeChatBridgeApprovals(ctx, kind, tr, cfg)
	s.pushChatBridgeApprovals(ctx, kind, tr, cfg, b)
}

func (s *Server) pushChatBridgeApprovals(ctx context.Context, kind string, tr chatbridge.Transport, cfg map[string]any, b *db.ChatBridge) {
	tag := chatBridgeLogTag(kind)
	// Push only where someone can press: an allowed chat, and for a group
	// (or a chat of unknown type) only when at least one user is allowed.
	pushes, err := s.m.PG().ChatBridgeApprovalsToPush(ctx, kind, b.AllowedChats, len(b.AllowedUsers) > 0, chatBridgeApprovalBatch)
	if err != nil {
		log.Printf("%s list approval requests to push: %v", tag, err)
		return
	}
	for _, p := range pushes {
		nonce, err := newApprovalNonce()
		if err != nil {
			log.Printf("%s approval nonce: %v", tag, err)
			return
		}
		text := s.approvalMessage(p, b.ShareText)
		id, created, err := s.m.PG().CreateChatBridgeApproval(ctx, kind, p.ChatID, p.PendingID, approvalNonceHash(nonce), text, time.Now().Add(chatBridgeApprovalTTL))
		if err != nil {
			log.Printf("%s record approval push of request %d: %v", tag, p.PendingID, err)
			continue
		}
		if !created {
			continue // pushed by someone else in the meantime
		}
		sendCtx, cancel := context.WithTimeout(ctx, chatBridgeSendWait)
		msgID, err := tr.SendButtons(sendCtx, cfg, p.ChatID, text, [][]chatbridge.Button{{
			{Text: "Allow", Data: approvalData(nonce, true)},
			{Text: "Block", Data: approvalData(nonce, false)},
		}})
		cancel()
		if err != nil {
			log.Printf("%s approval request %d to chat %s not sent: %v", tag, p.PendingID, p.ChatID, err)
			_, _ = s.m.PG().CloseChatBridgeApproval(ctx, id, "send-failed")
			continue
		}
		if err := s.m.PG().SetChatBridgeApprovalMessage(ctx, id, msgID); err != nil {
			log.Printf("%s store message of approval request %d: %v", tag, p.PendingID, err)
			continue
		}
		log.Printf("%s approval request %d of task %s sent to chat %s", tag, p.PendingID, p.TaskID, p.ChatID)
	}
}

func (s *Server) closeChatBridgeApprovals(ctx context.Context, kind string, tr chatbridge.Transport, cfg map[string]any) {
	tag := chatBridgeLogTag(kind)
	rows, err := s.m.PG().ChatBridgeApprovalsToClose(ctx, kind, chatBridgeApprovalBatch)
	if err != nil {
		log.Printf("%s list approval messages to close: %v", tag, err)
		return
	}
	for _, a := range rows {
		reason, outcome := "expired", "The buttons expired. Decide in ARTEX."
		switch {
		case a.MessageID == "":
			reason, outcome = "send-failed", ""
		case a.PendingStatus != "pending":
			reason, outcome = "decided", "Decided in ARTEX: "+approvalStatusWord(a.PendingStatus)+"."
		}
		closed, err := s.m.PG().CloseChatBridgeApproval(ctx, a.ID, reason)
		if err != nil || !closed || outcome == "" {
			continue // a press was first, or there is no message to edit
		}
		sendCtx, cancel := context.WithTimeout(ctx, chatBridgeSendWait)
		if err := tr.EditText(sendCtx, cfg, a.ChatID, a.MessageID, a.MessageText+"\n\n"+outcome); err != nil {
			log.Printf("%s close approval message %s in chat %s: %v", tag, a.MessageID, a.ChatID, err)
		}
		cancel()
	}
}

// bridgeApprovalPress handles a press on Allow or Block.
func (s *Server) bridgeApprovalPress(ctx context.Context, req chatbridge.Request) chatbridge.CallbackReply {
	tag := chatBridgeLogTag(req.Kind)
	nonce, allow, ok := parseApprovalData(req.Args)
	if !ok || s.m.interceptor == nil {
		return chatbridge.CallbackReply{Toast: "This button is not recognized."}
	}
	by := bridgeApprover(req)
	a, err := s.m.PG().UseChatBridgeApproval(ctx, req.Kind, req.ChatID, req.MessageID, approvalNonceHash(nonce), by)
	if err != nil {
		log.Printf("%s check approval button in chat %s: %v", tag, req.ChatID, err)
		return chatbridge.CallbackReply{Toast: "Could not check the button. Try again."}
	}
	if a == nil {
		// Unknown, used, expired, or pressed in another chat or message.
		return chatbridge.CallbackReply{Toast: "This button no longer works. Decide in ARTEX."}
	}
	verb := map[bool]string{true: "Allowed", false: "Blocked"}[allow]
	var outcome string
	err = s.m.interceptor.Decide(a.PendingID, allow, by, "chat "+req.Kind+":"+req.ChatID)
	switch {
	case err == nil:
		outcome = verb + " by " + bridgeUserLabel(req) + "."
		log.Printf("%s user %s in chat %s %s approval request %d", tag, req.UserID, req.ChatID, strings.ToLower(verb), a.PendingID)
	case errors.Is(err, intercept.ErrAlreadyDecided):
		outcome = "Already decided in ARTEX."
	case errors.Is(err, db.ErrTargetPinEmpty):
		outcome = "Could not allow: the request lists no resolved address. Decide in ARTEX."
	default:
		log.Printf("%s decide approval request %d from chat %s: %v", tag, a.PendingID, req.ChatID, err)
		outcome = "Could not record the decision. Decide in ARTEX."
	}
	return chatbridge.CallbackReply{Toast: outcome, Edit: a.MessageText + "\n\n" + outcome}
}

// approvalMessage is the text of a push: fixed wording and identifiers. The
// request itself and the rule's or judge's reason are free-form, so they go
// out only with share_text, filtered.
func (s *Server) approvalMessage(p db.ChatBridgePush, share bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Approval needed · task #%s\nTool: %s", p.TaskID, oneLine(p.ToolName))
	if p.AgentName != "" {
		fmt.Fprintf(&b, " · %s", oneLine(p.AgentName))
	}
	if p.TargetKey != "" {
		fmt.Fprintf(&b, "\nTarget: %s", oneLine(p.TargetKey))
	}
	risk := intercept.ScoreRisk(p.ToolName, p.ToolInput)
	fmt.Fprintf(&b, "\nRisk: %s (%d)", risk.Level, risk.Score)
	if share {
		secrets := s.bridgeTaskSecrets(p.TaskID)
		if input := chatbridge.SafeText(oneLine(string(p.ToolInput)), secrets, chatBridgeApprovalExcerpt); input != "" && input != "{}" {
			fmt.Fprintf(&b, "\nRequest: %s", input)
		}
		if reason := chatbridge.SafeText(oneLine(p.Reason), secrets, chatBridgeApprovalExcerpt); reason != "" {
			fmt.Fprintf(&b, "\nReason: %s", reason)
		}
	}
	if link := s.bridgeApprovalsLink(); link != "" {
		fmt.Fprintf(&b, "\nDetails: %s", link)
	}
	fmt.Fprintf(&b, "\nThe buttons work once and expire in %d minutes.", int(chatBridgeApprovalTTL/time.Minute))
	return b.String()
}

func (s *Server) bridgeApprovalsLink() string {
	base := trimTrailingSlash(notifyPublicBaseURL(s.m.PG()))
	if base == "" {
		return ""
	}
	return base + "/system/intercept/approvals"
}

// bridgeApprover is the approver recorded with the decision, next to the
// web UI's account name: the chat app and the user's stable id.
func bridgeApprover(req chatbridge.Request) string {
	return req.Kind + ":" + req.UserID
}

func bridgeUserLabel(req chatbridge.Request) string {
	if req.UserName != "" {
		return "@" + req.UserName
	}
	return "user " + req.UserID
}

func approvalStatusWord(status string) string {
	switch status {
	case "allowed":
		return "allowed"
	case "denied":
		return "blocked"
	case "timeout":
		return "timed out"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

func newApprovalNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func approvalNonceHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

func approvalData(nonce string, allow bool) string {
	return approvalDataPrefix + ":" + nonce + ":" + map[bool]string{true: "a", false: "d"}[allow]
}

func parseApprovalData(data string) (nonce string, allow, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != approvalDataPrefix || len(parts[1]) != 22 {
		return "", false, false
	}
	switch parts[2] {
	case "a":
		return parts[1], true, true
	case "d":
		return parts[1], false, true
	}
	return "", false, false
}
