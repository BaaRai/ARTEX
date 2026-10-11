package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	actool "github.com/Autumn-27/norma/tool"
)

const askOperatorMaxImage = 10 << 20

var askOperatorImageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true}

// toolAskOperator lets a worker formally request operator input (captcha, OTP,
// 2FA, a decision): the question and an optional screenshot land in the intent's
// transcript as a user-role turn with an attachment card, and the intent pauses.
// The operator replies through the existing message-to-worker flow; the reply
// resumes the run with the answer as the next input (#41).
func (s *Server) toolAskOperator() actool.CoreTool {
	return wrTool("ask_operator",
		"Ask the human operator for input you cannot obtain yourself (a captcha solution, an email/SMS OTP code, a 2FA answer, a decision). The question appears in this intent's chat for the operator, optionally with a screenshot, and this intent then pauses; the operator's reply resumes this run with the answer as your next input. Ask precisely, and only when you are genuinely blocked.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "What you need from the operator, precisely (e.g. 'enter the 6-digit code sent to x@y', 'solve the captcha in the attached screenshot').",
				},
				"image_path": map[string]any{
					"type":        "string",
					"description": "Optional path to a screenshot (png/jpg/jpeg/webp/gif, max 10MB), e.g. the browser_take_screenshot output. It is copied into the task uploads and shown to the operator.",
				},
			},
			"required": []any{"question"},
		},
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var req struct {
				Question  string `json:"question"`
				ImagePath string `json:"image_path"`
			}
			_ = json.Unmarshal(in, &req)
			q := strings.TrimSpace(req.Question)
			if q == "" {
				return actool.Errorf("Error: question is required"), nil
			}
			ri := agent.RunInfoFrom(ctx)
			if ri.TaskID == 0 || ri.IntentID == 0 {
				return actool.Errorf("Error: ask_operator can only be used inside a worker intent run"), nil
			}
			taskID := fmt.Sprintf("%d", ri.TaskID)
			t := s.m.ResolveTask(taskID)
			if t == nil {
				return actool.Errorf("Error: task not found"), nil
			}
			att, err := stageAskOperatorImage(s.m.dir, taskID, req.ImagePath)
			if err != nil {
				return actool.Errorf("Error: " + err.Error()), nil
			}
			text := "OPERATOR INPUT REQUESTED: " + q + " (this intent is pausing; reply here to resume it with your answer)"
			act := userActivityWithAttachments("worker", text, att)
			node := ri.IntentID
			act.NodeID = &node
			s.engine.emitActivity(t, act)
			// The control call runs on a detached context: pausing may cancel the
			// run's context, and the pause bookkeeping itself must complete.
			if _, err := s.applyIntentControl(context.Background(), t, ri.IntentID, "pause", firstLine(q, 120), ""); err != nil {
				return actool.Errorf("Error: question delivered but pausing the intent failed: " + err.Error()), nil
			}
			return actool.Text("Question delivered to the operator. This intent is pausing now: finish your current step, record everything you already know as facts, and stop. The operator's reply will resume this run and arrive as your next input."), nil
		})
}

// stageAskOperatorImage copies the referenced image into the task's uploads dir
// (the same place chat uploads land, so the transcript attachment card resolves)
// and returns the attachment descriptor. Empty src returns no attachment.
func stageAskOperatorImage(dataDir, taskID, src string) ([]chatAttachment, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(src))
	if !askOperatorImageExt[ext] {
		return nil, fmt.Errorf("image_path must be a png/jpg/jpeg/webp/gif file, got %q", filepath.Base(src))
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("image not readable: %v", err)
	}
	if st.Size() > askOperatorMaxImage {
		return nil, fmt.Errorf("image too large (%s, max 10MB)", humanBytes(st.Size()))
	}
	dir := filepath.Join(dataDir, "tasks", taskID, "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("ask-operator-%d%s", time.Now().Unix(), ext)
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return nil, err
	}
	return []chatAttachment{{Name: name, Path: "uploads/" + name, Size: st.Size()}}, nil
}
