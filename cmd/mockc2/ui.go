package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/softsrv/rowbot/internal/concept2"
)

// registerUIRoutes adds the fixture-browser HTML UI: a listing of every
// result in the fixture file with a "Send" button per entry that fires it as
// a webhook at target.
func registerUIRoutes(mux *http.ServeMux, saved savedResults, byID map[int64]concept2.Result, dataPath, target string) {
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		entries := make([]entryView, 0, len(saved.Results))
		for _, res := range saved.Results {
			pretty, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				pretty = fmt.Appendf(nil, "(failed to render: %v)", err)
			}
			entries = append(entries, entryView{
				ID:       res.ID,
				Summary:  summarize(res),
				Pretty:   string(pretty),
				HasHR:    res.HeartRate != nil,
				NumSplit: len(res.Workout.Pieces()),
			})
		}

		data := indexData{
			DataPath: dataPath,
			Target:   target,
			Entries:  entries,
			Flash:    flashFromQuery(r),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTemplate.Execute(w, data); err != nil {
			log.Printf("mockc2: render index: %v", err)
		}
	})

	mux.HandleFunc("POST /send/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			redirectFlash(w, r, false, "invalid result id")
			return
		}
		result, ok := byID[id]
		if !ok {
			redirectFlash(w, r, false, fmt.Sprintf("result %d not found", id))
			return
		}

		status, err := sendWebhook(target, saved.Concept2UserID, result)
		if err != nil {
			redirectFlash(w, r, false, fmt.Sprintf("result %d: %v", id, err))
			return
		}
		redirectFlash(w, r, true, fmt.Sprintf("result %d sent — %s", id, status))
	})
}

// sendWebhook POSTs result as a webhook payload matching Concept2's
// documented {"data": {"type": "result-added", "result": {...}}} shape to
// target.
func sendWebhook(target string, userID int64, result concept2.Result) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"type": "result-added",
			"result": map[string]any{
				"id":      result.ID,
				"user_id": userID,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s responded %s: %s", target, resp.Status, bytes.TrimSpace(body))
	}
	return resp.Status, nil
}

// redirectFlash redirects to / carrying a one-shot flash message in the
// query string.
func redirectFlash(w http.ResponseWriter, r *http.Request, ok bool, text string) {
	v := url.Values{}
	if ok {
		v.Set("flash", "ok")
	} else {
		v.Set("flash", "err")
	}
	v.Set("msg", text)
	http.Redirect(w, r, "/?"+v.Encode(), http.StatusSeeOther)
}

func flashFromQuery(r *http.Request) *flashMsg {
	flash := r.URL.Query().Get("flash")
	if flash == "" {
		return nil
	}
	return &flashMsg{OK: flash == "ok", Text: r.URL.Query().Get("msg")}
}

// summarize builds the one-line summary shown next to each entry's Send
// button: id, date, activity type, distance, and time.
func summarize(r concept2.Result) string {
	return fmt.Sprintf("#%d  ·  %s  ·  %s  ·  %s  ·  %s",
		r.ID, r.Date, r.Type, formatDistance(r.Distance), formatDuration(r.Time))
}

func formatDuration(tenths int64) string {
	if tenths <= 0 {
		return "0:00.0"
	}
	t := tenths % 10
	totalSeconds := tenths / 10
	seconds := totalSeconds % 60
	minutes := totalSeconds / 60
	return fmt.Sprintf("%d:%02d.%d", minutes, seconds, t)
}

func formatDistance(metres float64) string {
	if metres >= 10000 {
		return fmt.Sprintf("%.1f km", metres/1000)
	}
	m := int64(metres)
	if m >= 1000 {
		return fmt.Sprintf("%d,%03d m", m/1000, m%1000)
	}
	return fmt.Sprintf("%d m", m)
}

type entryView struct {
	ID       int64
	Summary  string
	Pretty   string
	HasHR    bool
	NumSplit int
}

type flashMsg struct {
	OK   bool
	Text string
}

type indexData struct {
	DataPath string
	Target   string
	Entries  []entryView
	Flash    *flashMsg
}

var indexTemplate = template.Must(template.New("index").Parse(indexTemplateSrc))

const indexTemplateSrc = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>mockc2 — fixture browser</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    font-family: ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace;
    background: #0b0e12;
    color: #dde3ea;
    margin: 0;
    padding: 28px 32px 60px;
  }
  h1 { font-size: 16px; font-weight: 600; margin: 0 0 4px; }
  .sub { color: #7d8794; font-size: 12.5px; margin: 0 0 20px; }
  .flash {
    padding: 10px 14px;
    border-radius: 8px;
    margin-bottom: 18px;
    font-size: 13px;
  }
  .flash.ok { background: #123a24; border: 1px solid #1f6b41; color: #8be6ae; }
  .flash.err { background: #3a1616; border: 1px solid #6b2323; color: #f3a5a5; }
  .entry {
    border: 1px solid #232933;
    border-radius: 10px;
    margin-bottom: 12px;
    background: #12161c;
    overflow: hidden;
  }
  .entry-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 14px;
    border-bottom: 1px solid #232933;
  }
  .summary { font-size: 12.5px; color: #c3cad3; }
  .badges { display: flex; gap: 6px; margin-left: 10px; }
  .badge {
    font-size: 10.5px;
    padding: 1px 7px;
    border-radius: 999px;
    background: #1c2733;
    color: #7fb3e0;
    white-space: nowrap;
  }
  button {
    background: #2c6693;
    color: #fff;
    border: none;
    border-radius: 6px;
    padding: 6px 16px;
    font: inherit;
    font-size: 12.5px;
    cursor: pointer;
    flex-shrink: 0;
  }
  button:hover { background: #3a7fb5; }
  pre {
    margin: 0;
    padding: 12px 14px;
    max-height: 260px;
    overflow: auto;
    font-size: 11.5px;
    line-height: 1.5;
    color: #9aa4b0;
  }
</style>
</head>
<body>
  <h1>mockc2 fixture browser</h1>
  <p class="sub">{{len .Entries}} result(s) from {{.DataPath}} — Send posts to {{.Target}}</p>

  {{with .Flash}}
  <div class="flash {{if .OK}}ok{{else}}err{{end}}">{{.Text}}</div>
  {{end}}

  {{range .Entries}}
  <div class="entry">
    <div class="entry-header">
      <span class="summary">{{.Summary}}
        <span class="badges">
          {{if gt .NumSplit 0}}<span class="badge">{{.NumSplit}} splits</span>{{end}}
          {{if .HasHR}}<span class="badge">HR</span>{{end}}
        </span>
      </span>
      <form method="POST" action="/send/{{.ID}}">
        <button type="submit">Send</button>
      </form>
    </div>
    <pre>{{.Pretty}}</pre>
  </div>
  {{end}}
</body>
</html>
`
