package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
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
	case "search_tickets":
		return a.searchTickets(ctx, arguments)
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

	input := "Найди в интернете актуальную информацию по запросу: " + query + ". Ответь по-русски кратко и только на основе найденных источников."
	return a.searchWebInput(ctx, input, "low", 400, nil)
}

type ticketSearchArguments struct {
	Origin        string `json:"origin"`
	Destination   string `json:"destination"`
	DepartureDate string `json:"departure_date"`
	ReturnDate    string `json:"return_date"`
	Transport     string `json:"transport"`
	Passengers    int    `json:"passengers"`
}

func ticketField(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 80 {
		return "", fmt.Errorf("%s must contain 1 to 80 characters", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s contains an invalid character", name)
		}
	}
	return value, nil
}

func (a *api) searchTickets(ctx context.Context, arguments string) ([]byte, error) {
	var args ticketSearchArguments
	d := json.NewDecoder(strings.NewReader(arguments))
	d.DisallowUnknownFields()
	if err := d.Decode(&args); err != nil {
		return nil, errors.New("invalid ticket search arguments")
	}
	origin, err := ticketField(args.Origin, "origin")
	if err != nil {
		return nil, err
	}
	destination, err := ticketField(args.Destination, "destination")
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(origin, destination) {
		return nil, errors.New("origin and destination must differ")
	}
	departure, err := time.Parse("2006-01-02", strings.TrimSpace(args.DepartureDate))
	if err != nil {
		return nil, errors.New("departure_date must use YYYY-MM-DD")
	}
	transport := strings.TrimSpace(args.Transport)
	if transport == "" {
		transport = "both"
	}
	var transportText string
	switch transport {
	case "flight":
		transportText = "авиабилеты"
	case "train":
		transportText = "билеты на поезд"
	case "both":
		transportText = "авиабилеты и билеты на поезд"
	default:
		return nil, errors.New("transport must be flight, train, or both")
	}
	passengers := args.Passengers
	if passengers == 0 {
		passengers = 1
	}
	if passengers < 1 || passengers > 9 {
		return nil, errors.New("passengers must be between 1 and 9")
	}
	trip := fmt.Sprintf("%s из %s в %s на %s, %d пассажир(а)", transportText, origin, destination, departure.Format("2006-01-02"), passengers)
	if strings.TrimSpace(args.ReturnDate) != "" {
		returnDate, e := time.Parse("2006-01-02", strings.TrimSpace(args.ReturnDate))
		if e != nil || returnDate.Before(departure) {
			return nil, errors.New("return_date must be a valid YYYY-MM-DD date on or after departure_date")
		}
		trip += ", обратно " + returnDate.Format("2006-01-02")
	}
	input := "Найди в интернете актуальные цены по запросу: " + trip + ". Проверь билеты и тарифы на Яндекс Путешествиях, Aviasales, Tutu, Rasp, сайте РЖД и официальном сайте авиакомпании «Победа» https://ticket.flypobeda.ru/websky/. Ответь по-русски кратко, максимум тремя вариантами: перевозчик, цена и валюта, время отправления — только если это найдено в источнике. Отличай цену «от» от точной. Если цены не найдены, прямо скажи. Не выдумывай стоимость, расписание или наличие мест; покупку не оформляй. Укажи названия источников и предупреди, что цена может измениться."
	return a.searchWebInput(ctx, input, "low", 8000, nil)
}

func (a *api) searchWebInput(ctx context.Context, input, contextSize string, maxOutputTokens int, allowedDomains []string) ([]byte, error) {
	searchCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	webSearchTool := map[string]any{"type": "web_search", "search_context_size": contextSize}
	if len(allowedDomains) > 0 {
		webSearchTool["filters"] = map[string]any{"allowed_domains": allowedDomains}
	}
	body := map[string]any{
		"model":             a.model,
		"store":             false,
		"max_output_tokens": maxOutputTokens,
		"input":             input,
		"tools":             []any{webSearchTool},
		"include":           []string{"web_search_call.action.sources"},
	}
	if maxOutputTokens > 400 && strings.HasPrefix(a.model, "gpt-5") {
		body["reasoning"] = map[string]any{"effort": "low"}
	}
	data, err := a.post(searchCtx, "/responses", body, 2<<20)
	if err != nil {
		return nil, fmt.Errorf("web search failed: %w", err)
	}

	var response struct {
		Status            string `json:"status"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
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
		return nil, fmt.Errorf("web search returned no answer (status %s, reason %s)", response.Status, response.IncompleteDetails.Reason)
	}
	if runes := []rune(text); len(runes) > 3000 {
		text = string(runes[:3000])
	}
	return json.Marshal(webSearchToolOutput{Answer: text, Sources: sources})
}
