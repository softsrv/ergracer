package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

// Attachment is a single file to attach to a Discord channel message.
type Attachment struct {
	Filename string
	Data     []byte
}

// attachmentMeta describes one file in the payload_json "attachments" array,
// per Discord's multipart file-upload API.
type attachmentMeta struct {
	ID       int    `json:"id"`
	Filename string `json:"filename"`
}

// messagePayload is the JSON body carried in the payload_json part of a
// multipart message-with-attachment request.
type messagePayload struct {
	Content     string           `json:"content,omitempty"`
	Attachments []attachmentMeta `json:"attachments,omitempty"`
}

// SendChannelMessageWithAttachment posts a message with a single file
// attachment (e.g. a generated result image) to the given Discord channel,
// using multipart/form-data per Discord's file-upload API requirements.
func SendChannelMessageWithAttachment(ctx context.Context, client *http.Client, botToken, channelID, content string, attachment Attachment) error {
	if client == nil {
		client = http.DefaultClient
	}

	payload := messagePayload{
		Content: content,
		Attachments: []attachmentMeta{
			{ID: 0, Filename: attachment.Filename},
		},
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("discord send message with attachment: marshal payload: %w", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	payloadHeader := textproto.MIMEHeader{}
	payloadHeader.Set("Content-Disposition", `form-data; name="payload_json"`)
	payloadHeader.Set("Content-Type", "application/json")
	payloadPart, err := mw.CreatePart(payloadHeader)
	if err != nil {
		return fmt.Errorf("discord send message with attachment: create payload part: %w", err)
	}
	if _, err := payloadPart.Write(payloadJSON); err != nil {
		return fmt.Errorf("discord send message with attachment: write payload part: %w", err)
	}

	filePart, err := mw.CreateFormFile("files[0]", attachment.Filename)
	if err != nil {
		return fmt.Errorf("discord send message with attachment: create file part: %w", err)
	}
	if _, err := filePart.Write(attachment.Data); err != nil {
		return fmt.Errorf("discord send message with attachment: write file part: %w", err)
	}

	if err := mw.Close(); err != nil {
		return fmt.Errorf("discord send message with attachment: close multipart writer: %w", err)
	}

	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return fmt.Errorf("discord send message with attachment: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord send message with attachment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("discord send message with attachment: status %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}
	return nil
}
