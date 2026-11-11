// Package asr talks to the iFlytek input-method dictation service.
package asr

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const appKey = "15461402826c8360ebcf1270cbba88e5"
const appSecret = "sH8v2wSSxxHTy1UNfN6dFaSqVjlmR7BB"

// Replaced by tests with a local server.
var endpoint = url.URL{Scheme: "wss", Host: "100ime-iat-api.xfyun.cn", Path: "/v2/iat"}

type recognitionReply struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Status int `json:"status"`
		Result struct {
			SN  int    `json:"sn"`
			PGS string `json:"pgs"`
			RST string `json:"rst"`
			RG  []int  `json:"rg"`
			WS  []struct {
				CW []struct {
					W string `json:"w"`
				} `json:"cw"`
			} `json:"ws"`
		} `json:"result"`
	} `json:"data"`
}

func signedURL() string {
	u := endpoint
	date := time.Now().UTC().Format(http.TimeFormat)
	mac := hmac.New(sha256.New, []byte(appSecret))
	fmt.Fprintf(mac, "host: %s\ndate: %s\nGET %s HTTP/1.1", u.Host, date, u.Path)
	auth := fmt.Sprintf(`hmac username="%s", algorithm="hmac-sha256", headers="host date request-line", signature="%s"`, appKey, base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	u.RawQuery = url.Values{"host": {u.Host}, "date": {date}, "authorization": {base64.StdEncoding.EncodeToString([]byte(auth))}}.Encode()
	return u.String()
}

// audioFrame wraps a PCM chunk. status is 0 for the first frame of a
// connection (which also carries head: the common and business objects),
// 1 for the following ones and 2 for the empty end-of-audio frame.
func audioFrame(status int, audio []byte, head map[string]any) ([]byte, error) {
	frame := map[string]any{"data": map[string]any{"status": status, "format": "audio/L16;rate=16000", "encoding": "raw", "audio": base64.StdEncoding.EncodeToString(audio)}}
	for k, v := range head {
		frame[k] = v
	}
	return json.Marshal(frame)
}

// Replacement ranges refer to earlier sequence numbers, including partial hypotheses.
func mergeReply(segments map[int]string, reply recognitionReply) string {
	result := reply.Data.Result
	if result.PGS == "rpl" && len(result.RG) == 2 {
		for n := range segments {
			if n >= result.RG[0] && n <= result.RG[1] {
				delete(segments, n)
			}
		}
	}
	var text strings.Builder
	for _, word := range result.WS {
		if len(word.CW) > 0 {
			text.WriteString(word.CW[0].W)
		}
	}
	segments[result.SN] = text.String()
	numbers := make([]int, 0, len(segments))
	for n := range segments {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	text.Reset()
	for _, n := range numbers {
		text.WriteString(segments[n])
	}
	return text.String()
}

// connection is one WebSocket to the service. The service limits how long a
// connection may run and drops silent ones, so a dictation spans several.
type connection struct {
	conn  *websocket.Conn
	done  chan outcome // The reader's single report, sent as it exits.
	bytes int
	first bool
}

// outcome is what a connection recognized. final means the service itself
// closed the utterance (the speaker paused, or the end frame was answered).
// err is set only when retrying cannot help (the service rejected the
// request); a dropped link is not an error.
type outcome struct {
	text  string
	final bool
	err   error
}

func connect(ctx context.Context, prefix string, onText func(string, int)) (*connection, error) {
	// Reconnects must be quick; the microphone keeps queueing meanwhile.
	dialCtx, cancelDial := context.WithTimeout(ctx, 3*time.Second)
	defer cancelDial()
	conn, response, err := websocket.DefaultDialer.DialContext(dialCtx, signedURL(), nil)
	if err != nil {
		switch {
		case response != nil:
			return nil, fmt.Errorf("握手被拒绝 %s", response.Status)
		case dialCtx.Err() != nil && ctx.Err() == nil:
			return nil, errors.New("3 秒内没有连上")
		}
		return nil, err
	}
	c := &connection{conn: conn, done: make(chan outcome, 1), first: true}
	// Pings every 2 s keep an idle but healthy link from looking dead; only
	// 8 s without any frame at all means the link is gone.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	})
	stopClose := context.AfterFunc(ctx, func() { conn.Close() })
	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	go func() {
		defer close(heartbeatDone)
		defer stopClose()
		defer conn.Close()
		segments := make(map[int]string)
		text, stable := "", 0
		for {
			conn.SetReadDeadline(time.Now().Add(8 * time.Second))
			_, data, err := conn.ReadMessage()
			if err != nil {
				c.done <- outcome{text: text}
				return
			}
			var reply recognitionReply
			if err = json.Unmarshal(data, &reply); err != nil {
				c.done <- outcome{text: text, err: err}
				return
			}
			if reply.Code == 10118 {
				// The service ended a silent connection; the microphone keeps running.
				c.done <- outcome{text: text}
				return
			}
			if reply.Code != 0 {
				c.done <- outcome{text: text, err: fmt.Errorf("识别错误 %d：%s", reply.Code, reply.Message)}
				return
			}
			if len(reply.Data.Result.WS) > 0 {
				text = mergeReply(segments, reply)
				// A final sentence ("rlt") settles everything before it too.
				if reply.Data.Result.RST == "rlt" || reply.Data.Status == 2 {
					stable = len(text)
				}
				onText(prefix+text, len(prefix)+min(stable, len(text)))
			}
			if reply.Data.Status == 2 {
				c.done <- outcome{text: text, final: true}
				return
			}
		}
	}()
	return c, nil
}

// Recorder streams 16 kHz mono 16-bit PCM to send until ctx ends, calling ready
// once the microphone is open. Returning nil while ctx is still alive means
// the user is done speaking: the pending result is still fetched.
type Recorder func(ctx context.Context, send func([]byte) error, ready func()) error

// ErrSilence ends dictation after several connections closed without any text.
// The service closes a connection that hears no speech for about 10 s.
var ErrSilence = errors.New("长时间没有说话，已自动停止")

// errPaused stops the recorder once Options.EndPause is satisfied.
var errPaused = errors.New("speaker paused")

const maxEmptyConnections = 3

// Recognize keeps one capture alive across every connection. onText receives
// the whole transcript so far; text[:stable] is final and will not change.
// Dropped links are reconnected and the audio queued meanwhile is sent late.
func Recognize(ctx context.Context, options Options, record Recorder, onText func(text string, stable int), ready func()) error {
	return recognize(ctx, options, record, onText, ready, 50*32000)
}

func recognize(ctx context.Context, options Options, record Recorder, onText func(string, int), ready func(), connectionBytes int) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	head := map[string]any{
		"common":   map[string]any{"app_id": "100IME", "uid": "", "device_id": hex.EncodeToString(id[:])},
		"business": options.business(),
	}
	var current *connection
	var prefix string
	empty := 0
	defer func() {
		if current != nil {
			current.conn.Close()
		}
	}()
	// ended folds a finished connection into the transcript, all of it final now.
	ended := func(o outcome) error {
		current = nil
		if o.err != nil {
			return o.err
		}
		if o.text == "" {
			if empty++; empty >= maxEmptyConnections {
				return ErrSilence
			}
			return nil
		}
		empty = 0
		prefix += o.text
		onText(prefix, len(prefix))
		return nil
	}
	// finish asks the service for its final result and waits for it.
	finish := func() error {
		frame, err := audioFrame(2, nil, nil)
		if err == nil {
			err = current.conn.WriteMessage(websocket.TextMessage, frame)
		}
		if err != nil {
			current.conn.Close()
		}
		select {
		case o := <-current.done:
			return ended(o)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := record(ctx, func(audio []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if current != nil {
			select {
			case o := <-current.done:
				if err := ended(o); err != nil {
					return err
				}
				if options.EndPause > 0 && o.final && o.text != "" {
					return errPaused
				}
			default:
			}
		}
		if current != nil && current.bytes >= connectionBytes {
			if err := finish(); err != nil {
				return err
			}
		}
		// A chunk whose write fails is resent on a fresh connection, so audio
		// spoken while the link was down still reaches the recognizer.
		for attempt := 0; ; attempt++ {
			if current == nil {
				var err error
				if current, err = connect(ctx, prefix, onText); err != nil {
					return fmt.Errorf("连接识别服务失败：%w", err)
				}
			}
			status, frameHead := 1, map[string]any(nil)
			if current.first {
				status, frameHead = 0, head
			}
			frame, err := audioFrame(status, audio, frameHead)
			if err != nil {
				return err
			}
			if err = current.conn.WriteMessage(websocket.TextMessage, frame); err == nil {
				current.first = false
				current.bytes += len(audio)
				return nil
			}
			if attempt > 0 {
				return err
			}
			current.conn.Close()
			if err := ended(<-current.done); err != nil {
				return err
			}
		}
	}, ready)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil && !errors.Is(err, errPaused) {
		return err
	}
	if current != nil {
		// The recorder stopped: fetch what the service still owes us. Silence
		// is not worth reporting when the user ended the dictation themself.
		if err := finish(); err != nil && !errors.Is(err, ErrSilence) && ctx.Err() == nil {
			return err
		}
	}
	return nil
}
