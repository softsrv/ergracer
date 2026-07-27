package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendChannelMessageWithAttachment(t *testing.T) {
	const (
		testChannelID = "987654321"
		testToken     = "my-bot-token"
		testContent   = "New Rowing Result"
		testFilename  = "result.png"
	)
	testData := []byte("not-really-a-png-but-bytes-are-bytes")

	var gotContentType string
	var gotPayload messagePayload
	var gotFileName string
	var gotFileData []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/channels/" + testChannelID + "/messages"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bot "+testToken {
			t.Errorf("Authorization = %q, want %q", got, "Bot "+testToken)
		}

		gotContentType = r.Header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(gotContentType)
		if err != nil {
			t.Fatalf("parse content type: %v", err)
		}
		if !strings.HasPrefix(mediaType, "multipart/form-data") {
			t.Errorf("media type = %q, want multipart/form-data", mediaType)
		}
		boundary, ok := params["boundary"]
		if !ok || boundary == "" {
			t.Fatal("no boundary in Content-Type")
		}

		mr := multipart.NewReader(r.Body, boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			switch part.FormName() {
			case "payload_json":
				if ct := part.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("payload_json Content-Type = %q, want application/json", ct)
				}
				data, err := io.ReadAll(part)
				if err != nil {
					t.Fatalf("read payload_json part: %v", err)
				}
				if err := json.Unmarshal(data, &gotPayload); err != nil {
					t.Fatalf("unmarshal payload_json: %v", err)
				}
			case "files[0]":
				gotFileName = part.FileName()
				data, err := io.ReadAll(part)
				if err != nil {
					t.Fatalf("read files[0] part: %v", err)
				}
				gotFileData = data
			default:
				t.Errorf("unexpected part: %q", part.FormName())
			}
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origBase := discordAPIBase
	discordAPIBase = srv.URL
	defer func() { discordAPIBase = origBase }()

	err := SendChannelMessageWithAttachment(context.Background(), srv.Client(), testToken, testChannelID, testContent, Attachment{
		Filename: testFilename,
		Data:     testData,
	})
	if err != nil {
		t.Fatalf("SendChannelMessageWithAttachment: %v", err)
	}

	wantPayload := messagePayload{
		Content: testContent,
		Attachments: []attachmentMeta{
			{ID: 0, Filename: testFilename},
		},
	}
	if gotPayload.Content != wantPayload.Content {
		t.Errorf("payload content = %q, want %q", gotPayload.Content, wantPayload.Content)
	}
	if len(gotPayload.Attachments) != 1 || gotPayload.Attachments[0] != wantPayload.Attachments[0] {
		t.Errorf("payload attachments = %+v, want %+v", gotPayload.Attachments, wantPayload.Attachments)
	}

	if gotFileName != testFilename {
		t.Errorf("file name = %q, want %q", gotFileName, testFilename)
	}
	if !bytes.Equal(gotFileData, testData) {
		t.Errorf("file data = %q, want %q", gotFileData, testData)
	}
}

func TestSendChannelMessageWithAttachment_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401: Unauthorized"}`))
	}))
	defer srv.Close()

	origBase := discordAPIBase
	discordAPIBase = srv.URL
	defer func() { discordAPIBase = origBase }()

	err := SendChannelMessageWithAttachment(context.Background(), srv.Client(), "tok", "chan", "hi", Attachment{
		Filename: "result.png",
		Data:     []byte("data"),
	})
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}
