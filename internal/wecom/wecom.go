package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	responseBodyLimit = 1 << 20
	defaultTimeout    = 15 * time.Second
)

type MarkdownPayload struct {
	Content string
}

type Client struct {
	webhook string
	http    *http.Client
}

func New(webhook string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{webhook: webhook, http: httpClient}
}

func BuildOriginalMarkdown(qid, content string) MarkdownPayload {
	return MarkdownPayload{Content: strings.Join([]string{
		"# WxPusher 通知",
		"",
		"**消息 ID**: " + qid,
		"",
		"**原始内容**:",
		content,
	}, "\n")}
}

func BuildEnrichedMarkdown(qid, pageURL, title, summary, screenshotPath string) MarkdownPayload {
	return MarkdownPayload{Content: strings.Join([]string{
		"# WxPusher 链接富化",
		"",
		"**消息 ID**: " + qid,
		"**URL**: " + pageURL,
		"**标题**: " + title,
		"",
		"**摘要**:",
		summary,
		"",
		"**截图路径**: " + screenshotPath,
	}, "\n")}
}

func (c *Client) Send(ctx context.Context, payload MarkdownPayload) error {
	body, err := json.Marshal(struct {
		MsgType  string          `json:"msgtype"`
		Markdown MarkdownPayload `json:"markdown"`
	}{
		MsgType:  "markdown",
		Markdown: payload,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhook, bytes.NewReader(body))
	if err != nil {
		return errors.New("create wecom request failed")
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("send wecom request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, responseBodyLimit))
		return fmt.Errorf("wecom send failed: status=%d", resp.StatusCode)
	}

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&result); err != nil {
		return fmt.Errorf("decode wecom response failed: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wecom send failed: errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func (c *Client) SendMarkdown(ctx context.Context, content string) error {
	return c.Send(ctx, MarkdownPayload{Content: content})
}
