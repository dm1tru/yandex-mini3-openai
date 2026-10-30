package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type voskEvent struct {
	Event  string `json:"event"`
	Mode   string `json:"mode"`
	PCM    string `json:"pcm"`
	Result struct {
		Text    string `json:"text"`
		Partial string `json:"partial"`
	} `json:"result"`
}

const followUpWindow = 20 * time.Second
const conversationFile = "/tmp/mini3-openai-conversation"

func (a *api) listenRealtime(ctx context.Context) error {
	h := startHardware(ctx)
	defer h.close()
	_ = os.Remove(muteFile)
	_ = os.Remove(conversationFile)
	w, err := connectRealtime(ctx, a.key, a.realtimeModel, a.voice)
	if err != nil {
		return err
	}
	defer w.conn.Close()

	cmd := exec.Command("/data/opt/bin/vosk-stream", "/data/opt/vosk/model-ru")
	wake := a.wakeWord
	if wake == "" {
		wake = "компьютер"
	}
	grammar, _ := json.Marshal([]string{wake, "[unk]"})
	cmd.Env = append(os.Environ(), "VOSK_MUTE_FILE="+muteFile, "VOSK_CONVERSATION_FILE="+conversationFile, "VOSK_WAKE_WORD="+wake, "VOSK_WAKE_GRAMMAR="+string(grammar), "VOSK_EMIT_AUDIO=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() }()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
		case <-done:
		}
	}()
	defer close(done)
	defer os.Remove(muteFile)
	defer os.Remove(conversationFile)
	fmt.Printf("Realtime connected. Say: %s, <question>. Voice is AI-generated.\n", wake)

	voskEvents := make(chan voskEvent, 32)
	voskErrors := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		s.Buffer(make([]byte, 8192), 64<<10)
		for s.Scan() {
			var ev voskEvent
			if json.Unmarshal(s.Bytes(), &ev) == nil {
				voskEvents <- ev
			}
		}
		if e := s.Err(); e != nil {
			voskErrors <- e
		} else {
			voskErrors <- errors.New("Vosk stream ended")
		}
	}()
	rtDone := make(chan realtimeEvent, 4)
	rtErrors := make(chan error, 1)
	go realtimeReceiver(ctx, w, a, h, rtDone, rtErrors)

	var preroll [][]byte
	active := false
	waitingResponse := false
	var followUpUntil time.Time
	var calls []time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case err = <-voskErrors:
			return err
		case err = <-rtErrors:
			return fmt.Errorf("Realtime: %w", err)
		case <-rtDone:
			_ = os.Remove(muteFile)
			active = true
			waitingResponse = false
			followUpUntil = time.Now().Add(followUpWindow)
			h.setConversationMode(true)
			if err = os.WriteFile(conversationFile, []byte("active\n"), 0600); err != nil {
				return err
			}
			h.set("listening", followUpWindow)
		case ev := <-voskEvents:
			switch ev.Event {
			case "ready", "stats":
				fmt.Println("Vosk:", ev.Event)
			case "audio":
				pcm, e := base64.StdEncoding.DecodeString(ev.PCM)
				if e != nil {
					continue
				}
				if active && !waitingResponse && !followUpUntil.IsZero() && time.Now().After(followUpUntil) {
					active = false
					followUpUntil = time.Time{}
					preroll = nil
					h.setConversationMode(false)
					_ = os.Remove(conversationFile)
					h.set("idle", 0)
				}
				preroll = append(preroll, pcm)
				if len(preroll) > 24 {
					preroll = preroll[len(preroll)-24:]
				}
				if active {
					resampled, e := pcm16to24(pcm)
					if e == nil && len(resampled) > 0 {
						if e = w.appendAudio(resampled); e != nil {
							return e
						}
					}
				}
			case "mode":
				if ev.Mode == "listening" && !active && !waitingResponse {
					active = true
					followUpUntil = time.Now().Add(followUpWindow)
					h.setConversationMode(true)
					if e := os.WriteFile(conversationFile, []byte("active\n"), 0600); e != nil {
						return e
					}
					h.set("listening", 0)
					for _, chunk := range preroll {
						resampled, e := pcm16to24(chunk)
						if e != nil {
							return e
						}
						if len(resampled) > 0 {
							if e = w.appendAudio(resampled); e != nil {
								return e
							}
						}
					}
					preroll = nil
				} else if ev.Mode == "wake" && !active && !waitingResponse {
					h.set("idle", 0)
				}
			case "partial":
				if active && !waitingResponse && strings.TrimSpace(ev.Result.Partial) != "" {
					followUpUntil = time.Now().Add(followUpWindow)
					h.set("listening", followUpWindow)
				}
				if strings.Contains(strings.ToLower(ev.Result.Partial), wake) {
					h.set("listening", 10*time.Second)
				}
			case "final":
				if !active {
					continue
				}
				text := strings.TrimSpace(ev.Result.Text)
				if text == "" {
					continue
				}
				q, found := wakeQuestion(text, wake)
				if found && q == "" {
					followUpUntil = time.Now().Add(followUpWindow)
					h.set("listening", followUpWindow)
					continue
				}
				recent := calls[:0]
				for _, t := range calls {
					if time.Since(t) < time.Minute {
						recent = append(recent, t)
					}
				}
				calls = recent
				if len(calls) >= 6 {
					fmt.Fprintln(os.Stderr, "Rate limit: six questions per minute")
					_ = w.sendJSON(map[string]any{"type": "input_audio_buffer.clear"})
					active = false
					followUpUntil = time.Time{}
					h.setConversationMode(false)
					_ = os.Remove(conversationFile)
					h.set("idle", 0)
					continue
				}
				calls = append(calls, time.Now())
				if e := w.sendJSON(map[string]any{"type": "input_audio_buffer.commit"}); e != nil {
					return e
				}
				if e := w.sendJSON(map[string]any{"type": "response.create"}); e != nil {
					return e
				}
				active = false
				waitingResponse = true
				followUpUntil = time.Time{}
				_ = os.WriteFile(muteFile, []byte("busy\n"), 0600)
				h.set("thinking", 0)
				fmt.Println("Question sent as audio to Realtime")
			}
		}
	}
}
