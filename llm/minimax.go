package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	minimaxEndpoint = "https://api.minimax.io/v1/text/chatcompletion_v2"
	// V0 estimated price (USD/1k tokens); calibrate against real billing in V1.
	costPerKTokenIn  = 0.0003
	costPerKTokenOut = 0.0011
)

type minimaxClient struct {
	apiKey      string
	model       string
	endpoint    string
	http        *http.Client
	maxTokens   int      // 0 = 不发
	temperature *float64 // nil = 不发
}

// NewMiniMax constructs a MiniMax M2.7 HTTP client (design-v3 §4 LLM Provider).
// If model is empty, defaults to "MiniMax-M2.7".
// Endpoint defaults to minimaxEndpoint; timeout defaults to 60 s；不发 max_tokens/temperature。
func NewMiniMax(apiKey, model string) Client {
	if model == "" {
		model = "MiniMax-M2.7"
	}
	return newMiniMaxFull(apiKey, model, minimaxEndpoint, 60*time.Second, 0, nil)
}

// newMiniMaxFull is the internal constructor used by NewMiniMaxFromConfig.
// NOTE(T0/T8): endpoint URL, auth header, and JSON field names are structurally
// correct anchors based on typical MiniMax API conventions. Verify against the
// official MiniMax docs before production use — fields may differ from actual API.
// maxTokens<=0 / temperature==nil 时不发对应字段（保持现状）。
func newMiniMaxFull(apiKey, model, endpoint string, timeout time.Duration, maxTokens int, temperature *float64) Client {
	return &minimaxClient{
		apiKey:      apiKey,
		model:       model,
		endpoint:    endpoint,
		http:        &http.Client{Timeout: timeout},
		maxTokens:   maxTokens,
		temperature: temperature,
	}
}

func (c *minimaxClient) Model() string { return c.model }

type chatReq struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`  // 0 省略
	Temperature *float64  `json:"temperature,omitempty"` // nil 省略
}

type message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"` // 推理模型思考通道；content 空时回退
}

type chatResp struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// Call sends prompt to MiniMax and returns (response, tokIn, tokOut, costUSD, error).
// Returns an error for non-2xx HTTP status or a non-zero base_resp.status_code.
func (c *minimaxClient) Call(ctx context.Context, prompt string) (string, int, int, float64, error) {
	body, _ := json.Marshal(chatReq{
		Model:       c.model,
		Messages:    []message{{Role: "user", Content: prompt}},
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return "", 0, 0, 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", 0, 0, 0, fmt.Errorf("minimax http %d: %s", res.StatusCode, string(raw))
	}

	var r chatResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", 0, 0, 0, fmt.Errorf("decode: %w; body=%s", err, string(raw))
	}
	if r.BaseResp.StatusCode != 0 {
		return "", 0, 0, 0, fmt.Errorf("minimax api error %d: %s", r.BaseResp.StatusCode, r.BaseResp.StatusMsg)
	}
	if len(r.Choices) == 0 {
		return "", 0, 0, 0, fmt.Errorf("minimax: empty choices; body=%s", string(raw))
	}
	tokIn, tokOut := r.Usage.PromptTokens, r.Usage.CompletionTokens
	cost := float64(tokIn)/1000*costPerKTokenIn + float64(tokOut)/1000*costPerKTokenOut
	msg := r.Choices[0].Message
	content := msg.Content
	if content == "" && msg.ReasoningContent != "" {
		// 推理模型把答案留在思考通道、content 空：回退取 reasoning_content。
		content = msg.ReasoningContent
	}
	if content == "" {
		// 仍空 → 返回可诊断错误（带原始 body），不再静默空串（2026-06-25 claim_coverage 空回复实证）。
		return "", tokIn, tokOut, cost, fmt.Errorf("minimax: empty content（content 与 reasoning_content 均空）; body=%s", string(raw))
	}
	return content, tokIn, tokOut, cost, nil
}
