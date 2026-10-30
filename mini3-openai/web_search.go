package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type realtimeSearchArguments struct {
	Query string `json:"query"`
}

type webSearchCitation struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type webSearchToolOutput struct {
	Answer  string              `json:"answer,omitempty"`
	Sources []webSearchCitation `json:"sources,omitempty"`
	Error   string              `json:"error,omitempty"`
}

func (a *api) runRealtimeFunction(ctx context.Context, name, arguments string, h *hardware) ([]byte, error) {
	switch name {
	case "set_volume":
		var args struct {
			Percent *int `json:"percent"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return nil, errors.New("invalid volume arguments")
		}
		if args.Percent == nil {
			return nil, errors.New("volume percent is required")
		}
		level, err := h.setMasterVolume(ctx, *args.Percent)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"status": "ok", "volume_percent": level})
	case "adjust_volume":
		var args struct {
			Direction string `json:"direction"`
			Steps     int    `json:"steps"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return nil, errors.New("invalid volume adjustment arguments")
		}
		steps := args.Steps
		if steps == 0 {
			steps = 1
		}
		if steps < 1 || steps > 4 {
			return nil, errors.New("volume adjustment steps must be between 1 and 4")
		}
		delta := 5 * steps
		if args.Direction == "down" {
			delta = -delta
		} else if args.Direction != "up" {
			return nil, errors.New("volume adjustment direction must be up or down")
		}
		level, err := h.adjustMasterVolume(ctx, delta)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"status": "ok", "volume_percent": level})
	case "web_search":
		return a.searchWeb(ctx, arguments)
	default:
		return nil, fmt.Errorf("unsupported tool: %s", name)
	}
}

func (a *api) searchWeb(ctx context.Context, arguments string) ([]byte, error) {
	var args realtimeSearchArguments
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, errors.New("invalid web search arguments")
	}
	query := strings.TrimSpace(args.Query)
	if query == "" || len([]rune(query)) > 600 {
		return nil, errors.New("search query must contain 1 to 600 characters")
	}

	searchCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	input := "Найди в интернете актуальную информацию по запросу: " + query + ". Ответь по-русски кратко и только на основе найденных источников."
	body := map[string]any{
		"model":             a.model,
		"store":             false,
		"max_output_tokens": 400,
		"input":             input,
		"tools":             []any{map[string]any{"type": "web_search", "search_context_size": "low"}},
		"include":           []string{"web_search_call.action.sources"},
	}
	data, err := a.post(searchCtx, "/responses", body, 2<<20)
	if err != nil {
		return nil, fmt.Errorf("web search failed: %w", err)
	}

	var response struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Action  struct {
				Sources []webSearchCitation `json:"sources"`
			} `json:"action"`
			Content []struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Annotations []struct {
					Type  string `json:"type"`
					Title string `json:"title"`
					URL   string `json:"url"`
				} `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode web search response: %w", err)
	}
	var answer strings.Builder
	var sources []webSearchCitation
	seen := make(map[string]bool)
	addSource := func(source webSearchCitation) {
		if len(sources) >= 5 || source.URL == "" || seen[source.URL] {
			return
		}
		u, err := url.Parse(source.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return
		}
		seen[source.URL] = true
		if strings.TrimSpace(source.Title) == "" {
			source.Title = u.Host
		}
		sources = append(sources, source)
	}
	for _, item := range response.Output {
		if item.Type == "web_search_call" {
			for _, source := range item.Action.Sources {
				addSource(source)
			}
		}
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "output_text" {
				continue
			}
			if answer.Len() > 0 {
				answer.WriteString("\n")
			}
			answer.WriteString(strings.TrimSpace(content.Text))
			for _, annotation := range content.Annotations {
				if annotation.Type == "url_citation" {
					addSource(webSearchCitation{Title: annotation.Title, URL: annotation.URL})
				}
			}
		}
	}
	text := strings.TrimSpace(answer.String())
	if text == "" {
		return nil, fmt.Errorf("web search returned no answer (status %s)", response.Status)
	}
	if runes := []rune(text); len(runes) > 3000 {
		text = string(runes[:3000])
	}
	return json.Marshal(webSearchToolOutput{Answer: text, Sources: sources})
}
