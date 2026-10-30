package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const realtimeInstruction = "Ты — голосовой помощник в домашней колонке. Отвечай по-русски и кратко, обычно 1–3 предложениями. Для актуальных фактов и когда пользователь просит поиск в интернете используй инструмент web_search. Для поиска билетов уточни города отправления и назначения и дату; если не указан транспорт, ищи самолёт и поезд, если не указано число пассажиров — считай одного. Для поездки туда-обратно уточни дату возвращения. Используй search_tickets и называй только цены, найденные в источниках; уточняй, если цена ориентировочная или наличие нужно проверить у продавца. Не утверждай, что билет забронирован или куплен. Отвечай по найденным источникам; по возможности называй источники, но не зачитывай URL. Если поиск завершился ошибкой, скажи об этом и не выдумывай актуальные данные. Для точной установки громкости используй set_volume, для относительной — adjust_volume; после успешного изменения назови итоговую громкость. Если инструмент сообщил об ошибке, не утверждай, что действие выполнено."

func realtimeFunctionTool(name, description string, parameters map[string]any) map[string]any {
	return map[string]any{"type": "function", "name": name, "description": description, "parameters": parameters}
}

type wsClient struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

func connectRealtime(ctx context.Context, key, model, voice string) (*wsClient, error) {
	endpoint := "wss://api.openai.com/v1/realtime?model=" + url.QueryEscape(model)
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(u.Hostname(), port), &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()})
	if err != nil {
		return nil, fmt.Errorf("connect Realtime: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = conn.Close()
		}
	}()
	keyBytes := make([]byte, 16)
	if _, err = rand.Read(keyBytes); err != nil {
		return nil, err
	}
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)
	if _, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nAuthorization: Bearer %s\r\n\r\n", u.RequestURI(), u.Host, wsKey, key); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	req := &http.Request{Method: "GET"}
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, fmt.Errorf("Realtime handshake: %w", err)
	}
	defer resp.Body.Close()
	acceptHash := sha1.Sum([]byte(wsKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	wantAccept := base64.StdEncoding.EncodeToString(acceptHash[:])
	if resp.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") || resp.Header.Get("Sec-WebSocket-Accept") != wantAccept {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("Realtime handshake rejected (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}
	w := &wsClient{conn: conn, reader: br}
	go func() { <-ctx.Done(); _ = conn.Close() }()
	for {
		b, e := w.readMessage()
		if e != nil {
			return nil, fmt.Errorf("Realtime session start: %w", e)
		}
		var ev struct {
			Type  string `json:"type"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &ev) != nil {
			continue
		}
		if ev.Type == "error" {
			return nil, errors.New(ev.Error.Message)
		}
		if ev.Type == "session.created" {
			break
		}
	}
	update := map[string]any{"type": "session.update", "session": map[string]any{
		"type": "realtime", "output_modalities": []string{"audio"}, "instructions": fmt.Sprintf("%s Сегодня %s.", realtimeInstruction, time.Now().Format("2006-01-02")),
		"tools": []any{
			realtimeFunctionTool("web_search", "Search the public web for current facts and answer questions that require up-to-date information.", map[string]any{
				"type": "object",
				"properties": map[string]any{"query": map[string]any{"type": "string", "description": "Concise search query with relevant context."}},
				"required": []string{"query"},
				"additionalProperties": false,
			}),
			realtimeFunctionTool("search_tickets", "Search current public offers for air and train tickets. Ask for missing origin, destination, or departure date first. Use ISO dates (YYYY-MM-DD); return_date is only for round trips. If transport is omitted, search both flights and trains.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"origin": map[string]any{"type": "string", "description": "Departure city or station."},
					"destination": map[string]any{"type": "string", "description": "Arrival city or station."},
					"departure_date": map[string]any{"type": "string", "description": "Departure date in YYYY-MM-DD format."},
					"return_date": map[string]any{"type": "string", "description": "Return date in YYYY-MM-DD format for round trips; omit for one-way."},
					"transport": map[string]any{"type": "string", "enum": []string{"flight", "train", "both"}, "description": "Transport type. Defaults to both."},
					"passengers": map[string]any{"type": "integer", "minimum": 1, "maximum": 9, "description": "Number of passengers; defaults to 1."},
				},
				"required": []string{"origin", "destination", "departure_date"},
				"additionalProperties": false,
			}),
			realtimeFunctionTool("set_volume", "Set the speaker Master volume to an exact percentage.", map[string]any{
				"type": "object",
				"properties": map[string]any{"percent": map[string]any{"type": "integer", "minimum": 0, "maximum": 100, "description": "Target speaker volume in percent."}},
				"required": []string{"percent"},
				"additionalProperties": false,
			}),
			realtimeFunctionTool("adjust_volume", "Raise or lower speaker Master volume by 5 percentage points per step.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"direction": map[string]any{"type": "string", "enum": []string{"up", "down"}},
					"steps": map[string]any{"type": "integer", "minimum": 1, "maximum": 4, "description": "Number of 5-point steps. Defaults to 1."},
				},
				"required": []string{"direction"},
				"additionalProperties": false,
			}),
		},
		"tool_choice": "auto",
		"audio": map[string]any{
			"input":  map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "turn_detection": nil, "noise_reduction": map[string]any{"type": "far_field"}},
			"output": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "voice": voice},
		},
	}}
	if err = w.sendJSON(update); err != nil {
		return nil, err
	}
	for {
		b, e := w.readMessage()
		if e != nil {
			return nil, fmt.Errorf("Realtime session configuration: %w", e)
		}
		var ev struct {
			Type  string `json:"type"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &ev) != nil {
			continue
		}
		if ev.Type == "error" {
			return nil, errors.New(ev.Error.Message)
		}
		if ev.Type == "session.updated" {
			break
		}
	}
	failed = false
	return w, nil
}

func (w *wsClient) sendJSON(v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return w.writeFrame(1, b)
}
func (w *wsClient) appendAudio(pcm []byte) error {
	return w.sendJSON(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(pcm)})
}
func (w *wsClient) writeFrame(opcode byte, payload []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	var h [14]byte
	h[0] = 0x80 | opcode
	idx := 2
	switch n := len(payload); {
	case n < 126:
		h[1] = 0x80 | byte(n)
	case n <= 65535:
		h[1] = 0x80 | 126
		binary.BigEndian.PutUint16(h[2:4], uint16(n))
		idx = 4
	default:
		h[1] = 0x80 | 127
		binary.BigEndian.PutUint64(h[2:10], uint64(n))
		idx = 10
	}
	mask := h[idx : idx+4]
	if _, e := rand.Read(mask); e != nil {
		return e
	}
	idx += 4
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i&3]
	}
	if _, e := w.conn.Write(h[:idx]); e != nil {
		return e
	}
	_, e := w.conn.Write(masked)
	return e
}
func (w *wsClient) readMessage() ([]byte, error) {
	var message []byte
	for {
		var h [2]byte
		if _, e := io.ReadFull(w.reader, h[:]); e != nil {
			return nil, e
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
		if n == 126 {
			var x [2]byte
			if _, e := io.ReadFull(w.reader, x[:]); e != nil {
				return nil, e
			}
			n = uint64(binary.BigEndian.Uint16(x[:]))
		} else if n == 127 {
			var x [8]byte
			if _, e := io.ReadFull(w.reader, x[:]); e != nil {
				return nil, e
			}
			n = binary.BigEndian.Uint64(x[:])
		}
		if n > 16<<20 {
			return nil, errors.New("Realtime message too large")
		}
		var mask [4]byte
		if masked {
			if _, e := io.ReadFull(w.reader, mask[:]); e != nil {
				return nil, e
			}
		}
		p := make([]byte, int(n))
		if _, e := io.ReadFull(w.reader, p); e != nil {
			return nil, e
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i&3]
			}
		}
		switch op {
		case 8:
			return nil, io.EOF
		case 9:
			if e := w.writeFrame(10, p); e != nil {
				return nil, e
			}
			continue
		case 10:
			continue
		case 0:
			message = append(message, p...)
		case 1, 2:
			message = append(message[:0], p...)
		default:
			continue
		}
		if fin {
			return message, nil
		}
	}
}

func pcm16to24(pcm []byte) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, errors.New("odd PCM input length")
	}
	n := len(pcm) / 2
	if n == 0 {
		return nil, nil
	}
	outSamples := (n*3 + 1) / 2
	out := make([]byte, outSamples*2)
	for j := 0; j < outSamples; j++ {
		pos := j * 2
		lo, frac := pos/3, pos%3
		if lo >= n {
			lo = n - 1
			frac = 0
		}
		hi := lo + 1
		if hi >= n {
			hi = lo
		}
		a := int32(int16(binary.LittleEndian.Uint16(pcm[lo*2:])))
		b := int32(int16(binary.LittleEndian.Uint16(pcm[hi*2:])))
		s := int16((a*int32(3-frac) + b*int32(frac)) / 3)
		binary.LittleEndian.PutUint16(out[j*2:], uint16(s))
	}
	return out, nil
}

func pcm24ToSpeaker(pcm []byte) []byte {
	if len(pcm)%2 != 0 {
		return nil
	}
	out := make([]byte, len(pcm)*4)
	for i := 0; i < len(pcm)/2; i++ {
		s := binary.LittleEndian.Uint16(pcm[i*2:])
		for j := 0; j < 4; j++ {
			binary.LittleEndian.PutUint16(out[i*8+j*2:], s)
		}
	}
	return out
}

type realtimeEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Text  string `json:"text"`
	Item  struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	Response struct {
		Status string `json:"status"`
	} `json:"response"`
}

func realtimeReceiver(ctx context.Context, w *wsClient, a *api, h *hardware, events chan<- realtimeEvent, errs chan<- error) {
	var player *exec.Cmd
	var stdin io.WriteCloser
	muted := false
	var pendingFunctionCall *realtimeEvent
	finish := func() {
		if stdin != nil {
			_ = stdin.Close()
			stdin = nil
		}
		if player != nil {
			_ = player.Wait()
			player = nil
		}
		if muted {
			_ = os.Remove(muteFile)
			muted = false
		}
	}
	defer finish()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		b, e := w.readMessage()
		if e != nil {
			if ctx.Err() == nil {
				errs <- e
			}
			return
		}
		var ev realtimeEvent
		if json.Unmarshal(b, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "error":
			errs <- errors.New(ev.Error.Message)
			return
		case "response.output_audio.delta":
			pcm, e := base64.StdEncoding.DecodeString(ev.Delta)
			if e != nil {
				errs <- e
				return
			}
			if player == nil {
				_ = os.WriteFile(muteFile, []byte("busy\n"), 0600)
				muted = true
				h.set("speaking", 0)
				player = exec.Command("/usr/bin/aplay", "-q", "-D", "speakers", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2")
				stdin, e = player.StdinPipe()
				if e == nil {
					e = player.Start()
				}
				if e != nil {
					finish()
					errs <- e
					return
				}
			}
			if _, e = stdin.Write(pcm24ToSpeaker(pcm)); e != nil {
				finish()
				errs <- e
				return
			}
		case "response.output_audio_transcript.delta":
			if ev.Delta != "" {
				fmt.Print(ev.Delta)
			}
		case "response.output_item.done":
			if ev.Item.Type == "function_call" {
				call := ev
				pendingFunctionCall = &call
			}
		case "response.done":
			if ev.Response.Status != "completed" {
				errs <- fmt.Errorf("Realtime response status: %s", ev.Response.Status)
				return
			}
			finish()
			if pendingFunctionCall != nil {
				call := *pendingFunctionCall
				pendingFunctionCall = nil
				if call.Item.CallID == "" {
					errs <- errors.New("Realtime function call is missing call_id")
					return
				}
				h.set("thinking", 0)
				output, toolErr := a.runRealtimeFunction(ctx, call.Item.Name, call.Item.Arguments, h)
				if toolErr != nil {
					output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
				}
				if e := w.sendJSON(map[string]any{"type": "conversation.item.create", "item": map[string]any{
					"type": "function_call_output", "call_id": call.Item.CallID, "output": string(output),
				}}); e != nil {
					errs <- e
					return
				}
				if e := w.sendJSON(map[string]any{"type": "response.create", "response": map[string]any{"tool_choice": "none"}}); e != nil {
					errs <- e
					return
				}
				continue
			}
			h.set("idle", 0)
			events <- ev
		}
	}
}
