package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// localClient talks to any OpenAI-compatible chat server (Ollama, LM Studio, llama.cpp, vLLM),
// so the brain can run on your own GPU for free.
type localClient struct {
	baseURL string
	apiKey  string
	http    http.Client
}

func (c *localClient) chat(ctx context.Context, o callOpts) (string, error) {
	user := []map[string]any{}
	if o.imageJPEG != "" {
		user = append(user, map[string]any{"type": "image_url",
			"image_url": map[string]string{"url": "data:image/jpeg;base64," + o.imageJPEG}})
	}
	text := o.text
	if o.schema != nil {
		// Small local models follow an explicit schema in the prompt more reliably than server-side schemas.
		text += "\n\nReply with ONE JSON object only, matching this JSON schema:\n" + string(o.schema)
	}
	user = append(user, map[string]any{"type": "text", "text": text})

	body := map[string]any{
		"model": o.model,
		"messages": []map[string]any{
			{"role": "system", "content": o.system},
			{"role": "user", "content": user},
		},
		"max_tokens":  min(o.maxTokens, 2000),
		"temperature": 0.4,
		"stream":      false,
	}
	if o.schema != nil {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	key := c.apiKey
	if key == "" {
		key = "local"
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("local model server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("local model server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 300)])))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return "", errors.New("local model server sent an unexpected response")
	}
	reply := strings.TrimSpace(out.Choices[0].Message.Content)
	if o.schema != nil {
		reply = extractJSON(reply)
	}
	return reply, nil
}

// extractJSON returns the first complete JSON object in s (small models like to add chatter or ``` fences).
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr && esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case inStr && c == '"':
			inStr = false
		case inStr:
		case c == '"':
			inStr = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s
}
