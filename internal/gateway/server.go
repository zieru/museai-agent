package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"musegateway/internal/account"
	"musegateway/internal/config"
	"musegateway/internal/harness"
	"musegateway/internal/multimodal"
)

type Server struct {
	cfg  *config.GatewayConfig
	pool *account.Pool
}

func NewServer(cfg *config.GatewayConfig, pool *account.Pool) *Server {
	return &Server{
		cfg:  cfg,
		pool: pool,
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Root health check
	mux.HandleFunc("/health", s.handleHealth)

	// Models endpoints
	mux.HandleFunc("/api/v1/models", s.handleModels)
	mux.HandleFunc("/v1/models", s.handleModels)

	// OpenAI Chat Completions (text + vision multimodal)
	mux.HandleFunc("/api/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)

	// Anthropic Messages (Claude Code integration)
	mux.HandleFunc("/api/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("/api/v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleCountTokens)

	// Audio / Speech endpoint
	mux.HandleFunc("/api/v1/audio/speech", s.handleSpeech)
	mux.HandleFunc("/v1/audio/speech", s.handleSpeech)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "musegateway",
		"time":    time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"id":       "muse/muse_01",
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "muse",
			},
			{
				"id":       "muse",
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "muse",
			},
			{
				"id":       "claude-3-5-sonnet-20241022",
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "anthropic",
			},
		},
	})
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]interface{}{
		"input_tokens": 100,
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req OpenAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// 1. Determine requested account or default model
	accountID := ""
	if strings.HasPrefix(req.Model, "muse/") {
		parts := strings.Split(req.Model, "/")
		if len(parts) > 1 && parts[1] != "" {
			accountID = parts[1]
		}
	}
	if hAcc := r.Header.Get("X-Account-ID"); hAcc != "" {
		accountID = hAcc
	}

	acc, err := s.pool.GetAccount(accountID)
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	// 2. Extract last user message and multimodal contents
	var lastUserPrompt string
	var attachedFiles []string
	tempDir := os.TempDir()

	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == "user" {
			// Plain string content
			if msg.ContentStr != "" {
				lastUserPrompt = msg.ContentStr
				break
			}
			// Multimodal block array
			if len(msg.ContentParts) > 0 {
				var texts []string
				for _, part := range msg.ContentParts {
					if part.Type == "text" {
						texts = append(texts, part.Text)
					} else if part.Type == "image_url" && part.ImageURL != nil {
						if imgPath, err := multimodal.ProcessImage(tempDir, part.ImageURL.URL); err == nil {
							attachedFiles = append(attachedFiles, imgPath)
						}
					}
				}
				lastUserPrompt = strings.Join(texts, "\n")
				break
			}
		}
	}

	if lastUserPrompt == "" && len(attachedFiles) == 0 {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "No user message content provided"})
		return
	}

	// 3. Dispatch to isolated account session
	timeout := time.Duration(s.cfg.TimeoutSec) * time.Second
	reply, err := acc.ExecutePrompt(r.Context(), lastUserPrompt, attachedFiles, timeout)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Clean up temporary image files
	for _, f := range attachedFiles {
		_ = os.Remove(f)
	}

	// 4. Return OpenAI Chat Completion response
	resp := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-muse-%d", time.Now().Unix()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": reply,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     len(strings.Fields(lastUserPrompt)),
			"completion_tokens": len(strings.Fields(reply)),
			"total_tokens":      len(strings.Fields(lastUserPrompt)) + len(strings.Fields(reply)),
		},
	}

	sendJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnthropicMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Suggestion mode bypass for Claude Code CLI responsiveness
	for _, m := range req.Messages {
		if strings.Contains(m.ContentStr, "[SUGGESTION MODE:") {
			sendJSON(w, http.StatusOK, map[string]interface{}{
				"id":           fmt.Sprintf("msg_%d", time.Now().Unix()),
				"type":         "message",
				"role":         "assistant",
				"content":      []interface{}{},
				"model":        req.Model,
				"stop_reason":  "end_turn",
				"stop_sequence": nil,
				"usage":        map[string]int{"input_tokens": 10, "output_tokens": 0},
			})
			return
		}
	}

	acc, err := s.pool.GetAccount("")
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	var lastUserPrompt string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastUserPrompt = req.Messages[i].ContentStr
			break
		}
	}

	timeout := time.Duration(s.cfg.TimeoutSec) * time.Second
	reply, err := acc.ExecutePrompt(r.Context(), lastUserPrompt, nil, timeout)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Convert code fences into Claude tool_use blocks if applicable
	tools, cleanText := harness.ExtractToolCalls(reply)
	var contentBlocks []interface{}

	if cleanText != "" {
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type": "text",
			"text": cleanText,
		})
	}

	for i, t := range tools {
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type":  "tool_use",
			"id":    fmt.Sprintf("toolu_%d_%d", time.Now().Unix(), i),
			"name":  t.Name,
			"input": t.Input,
		})
	}

	stopReason := "end_turn"
	if len(tools) > 0 {
		stopReason = "tool_use"
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"id":           fmt.Sprintf("msg_muse_%d", time.Now().Unix()),
		"type":         "message",
		"role":         "assistant",
		"content":      contentBlocks,
		"model":        req.Model,
		"stop_reason":  stopReason,
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  len(strings.Fields(lastUserPrompt)),
			"output_tokens": len(strings.Fields(reply)),
		},
	})
}

func (s *Server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, map[string]string{
		"message": "TTS speech synthesizer endpoint ready",
	})
}

func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
