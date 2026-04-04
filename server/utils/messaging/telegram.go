package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var telegramHTTPClient = &http.Client{Timeout: 10 * time.Second}

// SendTelegramMessage sends a message via Telegram Bot API using HTTP.
// chatID is the numeric chat ID (string), botToken is the bot token.
func SendTelegramMessage(botToken, chatID, text string) error {
	if botToken == "" {
		return fmt.Errorf("Telegram Bot Token未配置")
	}
	if chatID == "" {
		return fmt.Errorf("Telegram Chat ID为空")
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("构造Telegram请求失败: %w", err)
	}

	resp, err := telegramHTTPClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Telegram API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("Telegram API返回错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析Telegram响应失败: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("Telegram API返回失败: %s", result.Description)
	}

	return nil
}
