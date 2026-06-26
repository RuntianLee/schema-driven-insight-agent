package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// anthropicVersion 是 Anthropic Messages API 必发的版本头。
const anthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens：Anthropic max_tokens 必填且须 >0；cfg 未设时的默认上限。
const anthropicDefaultMaxTokens = 4096

type anthropicReq struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"` // 必填 >0
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`    // "user"
	Content string `json:"content"` // string 形式，Anthropic 接受
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"` // "text" | "thinking" ...
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Type  string `json:"type"` // 错误时 "error"
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// callAnthropic sends prompt via the Anthropic Messages API wire format.
// 头：x-api-key + anthropic-version + content-type（不发 Authorization Bearer）。
// max_tokens 必填：c.maxTokens<=0 时默认 anthropicDefaultMaxTokens。
// 空响应带原始 body 报错（沿用现有空响应纪律，不静默空串）。
func (c *minimaxClient) callAnthropic(ctx context.Context, prompt string) (string, int, int, float64, error) {
	maxTokens := c.maxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	body, _ := json.Marshal(anthropicReq{
		Model:       c.model,
		MaxTokens:   maxTokens,
		Messages:    []anthropicMessage{{Role: "user", Content: prompt}},
		Temperature: c.temperature,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, 0, 0, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return "", 0, 0, 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", 0, 0, 0, fmt.Errorf("anthropic http %d: %s", res.StatusCode, string(raw))
	}

	var r anthropicResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", 0, 0, 0, fmt.Errorf("decode: %w; body=%s", err, string(raw))
	}
	if r.Type == "error" {
		return "", 0, 0, 0, fmt.Errorf("anthropic api error %s: %s; body=%s", r.Error.Type, r.Error.Message, string(raw))
	}

	tokIn, tokOut := r.Usage.InputTokens, r.Usage.OutputTokens
	cost := float64(tokIn)/1000*costPerKTokenIn + float64(tokOut)/1000*costPerKTokenOut

	var sb strings.Builder
	for _, block := range r.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	content := sb.String()
	if content == "" {
		// 无 text 块 / 全空 → 返回可诊断错误（带原始 body），不静默空串。
		return "", tokIn, tokOut, cost, fmt.Errorf("anthropic: empty content（无 text 块或全空）; body=%s", string(raw))
	}
	return content, tokIn, tokOut, cost, nil
}
