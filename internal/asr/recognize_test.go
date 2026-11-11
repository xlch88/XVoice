package asr

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestIncrementalReplacement(t *testing.T) {
	segments := make(map[int]string)
	for i, s := range []string{
		`{"data":{"result":{"sn":1,"pgs":"apd","ws":[{"cw":[{"w":"早"}]}]}}}`,
		`{"data":{"result":{"sn":2,"pgs":"rpl","rg":[1,1],"ws":[{"cw":[{"w":"早上好"}]}]}}}`,
		`{"data":{"result":{"sn":3,"pgs":"apd","ws":[{"cw":[{"w":"。"}]}]}}}`,
	} {
		var reply recognitionReply
		if err := json.Unmarshal([]byte(s), &reply); err != nil {
			t.Fatal(err)
		}
		text := mergeReply(segments, reply)
		if i == 2 && text != "早上好。" {
			t.Fatal(text)
		}
	}
}

func TestBusinessNeverMixesModelFamilies(t *testing.T) {
	for _, c := range []struct {
		options Options
		want    string
	}{
		{Options{}, `{"aue":"raw","dwa":"wpgs","ent":"sms16k","vad_enable":false}`},
		{Options{Domain: "medical"}, `{"aue":"raw","dwa":"wpgs","ent":"sms16k","pd":"medical","vad_enable":false}`},
		// Only Mandarin has domains.
		{Options{Language: "henanese16k", Domain: "medical"}, `{"aue":"raw","dwa":"wpgs","ent":"henanese16k","vad_enable":false}`},
		{Options{Language: "ja_jp"}, `{"accent":"mandarin","aue":"raw","domain":"iat","dwa":"wpgs","language":"ja_jp","vad_enable":false}`},
		{Options{EndPause: 1500 * time.Millisecond}, `{"aue":"raw","dwa":"wpgs","ent":"sms16k","eos":1500,"vad_enable":false}`},
	} {
		got, _ := json.Marshal(c.options.business())
		if string(got) != c.want {
			t.Errorf("%+v:\n got %s\nwant %s", c.options, got, c.want)
		}
	}
}

// fakeService points the client at a local server that runs scripts[i] for
// the i-th connection (the last script repeats) and counts connections.
func fakeService(t *testing.T, scripts ...func(conn *websocket.Conn)) *atomic.Int32 {
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		scripts[min(int(connections.Add(1)), len(scripts))-1](conn)
	}))
	real := endpoint
	endpoint = url.URL{Scheme: "ws", Host: server.Listener.Addr().String(), Path: "/v2/iat"}
	t.Cleanup(func() {
		endpoint = real
		server.Close()
	})
	return &connections
}

// say sends one recognition reply; an empty text sends no words.
func say(conn *websocket.Conn, sn int, pgs string, rg []int, rst string, status int, text string) {
	result := map[string]any{"sn": sn, "pgs": pgs, "rg": rg, "rst": rst}
	if text != "" {
		result["ws"] = []any{map[string]any{"cw": []any{map[string]any{"w": text}}}}
	}
	conn.WriteJSON(map[string]any{"code": 0, "message": "success", "data": map[string]any{"status": status, "result": result}})
}

// hear reads client frames up to and including the one with this status and
// returns it.
func hear(conn *websocket.Conn, status float64) map[string]any {
	for {
		var frame map[string]any
		if conn.ReadJSON(&frame) != nil {
			return nil
		}
		if frame["data"].(map[string]any)["status"] == status {
			return frame
		}
	}
}

// speaker is a microphone that delivers count chunks, one every gap.
func speaker(count int, gap time.Duration) Recorder {
	return func(ctx context.Context, send func([]byte) error, ready func()) error {
		ready()
		for range count {
			if err := send(make([]byte, 1280)); err != nil {
				return err
			}
			time.Sleep(gap)
		}
		return nil
	}
}

type update struct {
	text   string
	stable int
}

func collect(updates *[]update) func(string, int) {
	return func(text string, stable int) { *updates = append(*updates, update{text, stable}) }
}

func TestStableFollowsFinalSentences(t *testing.T) {
	fakeService(t, func(conn *websocket.Conn) {
		first := hear(conn, 0)
		if first["business"].(map[string]any)["ent"] != "sms16k" || first["common"] == nil {
			t.Errorf("first frame lacks its head: %v", first)
		}
		say(conn, 1, "apd", nil, "pgs", 0, "你")
		say(conn, 2, "rpl", []int{1, 1}, "rlt", 1, "你好")
		say(conn, 3, "apd", nil, "pgs", 1, "世")
		hear(conn, 2)
		say(conn, 4, "rpl", []int{3, 3}, "rlt", 2, "，世界。")
	})
	var updates []update
	if err := recognize(context.Background(), Options{}, speaker(5, 20*time.Millisecond), collect(&updates), func() {}, 1<<30); err != nil {
		t.Fatal(err)
	}
	want := []update{{"你", 0}, {"你好", 6}, {"你好世", 6}, {"你好，世界。", 18}, {"你好，世界。", 18}}
	if len(updates) != len(want) {
		t.Fatalf("got %v", updates)
	}
	for i := range want {
		if updates[i] != want[i] {
			t.Errorf("update %d = %v, want %v", i, updates[i], want[i])
		}
	}
}

func TestDroppedLinkReconnectsAndKeepsTranscript(t *testing.T) {
	connections := fakeService(t, func(conn *websocket.Conn) {
		hear(conn, 0)
		say(conn, 1, "apd", nil, "pgs", 0, "第一句")
		// Returning drops the TCP connection without a close frame.
	}, func(conn *websocket.Conn) {
		hear(conn, 0)
		say(conn, 1, "apd", nil, "rlt", 0, "第二句")
		hear(conn, 2)
		say(conn, 2, "apd", nil, "rlt", 2, "")
	})
	var updates []update
	if err := recognize(context.Background(), Options{}, speaker(30, 10*time.Millisecond), collect(&updates), func() {}, 1<<30); err != nil {
		t.Fatal(err)
	}
	last := updates[len(updates)-1]
	if last.text != "第一句第二句" || last.stable != len(last.text) || connections.Load() != 2 {
		t.Fatalf("got %v over %d connections", last, connections.Load())
	}
}

func TestSilentConnectionsEndDictation(t *testing.T) {
	connections := fakeService(t, func(conn *websocket.Conn) {
		hear(conn, 0)
		// What the service sends after ~10 s without speech.
		say(conn, 1, "apd", nil, "rlt", 2, "")
	})
	err := recognize(context.Background(), Options{}, speaker(500, 5*time.Millisecond), func(string, int) {}, func() {}, 1<<30)
	if !errors.Is(err, ErrSilence) || connections.Load() != maxEmptyConnections {
		t.Fatalf("err = %v after %d connections", err, connections.Load())
	}
}

func TestServiceErrorIsNotRetried(t *testing.T) {
	connections := fakeService(t, func(conn *websocket.Conn) {
		hear(conn, 0)
		conn.WriteJSON(map[string]any{"code": 11200, "message": "licc failed"})
		hear(conn, 2)
	})
	err := recognize(context.Background(), Options{}, speaker(500, 5*time.Millisecond), func(string, int) {}, func() {}, 1<<30)
	if err == nil || !strings.Contains(err.Error(), "11200") || connections.Load() != 1 {
		t.Fatalf("err = %v after %d connections", err, connections.Load())
	}
}

func TestEndPauseStopsAfterFirstUtterance(t *testing.T) {
	connections := fakeService(t, func(conn *websocket.Conn) {
		first := hear(conn, 0)
		if first["business"].(map[string]any)["eos"] != 2000.0 {
			t.Errorf("eos not requested: %v", first["business"])
		}
		say(conn, 1, "apd", nil, "rlt", 1, "你好")
		say(conn, 2, "apd", nil, "rlt", 2, "。")
		hear(conn, 2)
	})
	var updates []update
	started := time.Now()
	err := recognize(context.Background(), Options{EndPause: 2 * time.Second}, speaker(500, 5*time.Millisecond), collect(&updates), func() {}, 1<<30)
	last := updates[len(updates)-1]
	if err != nil || last != (update{"你好。", 9}) || connections.Load() != 1 || time.Since(started) > 2*time.Second {
		t.Fatalf("err = %v, last = %v, %d connections, took %v", err, last, connections.Load(), time.Since(started))
	}
}

// TestLive dictates TEST_WAV (16 kHz mono 16-bit) to the real service at
// microphone speed. TEST_LANGUAGE and TEST_END_PAUSE select Options.
func TestLive(t *testing.T) {
	path := os.Getenv("TEST_WAV")
	if path == "" {
		t.Skip("set TEST_WAV to a 16 kHz mono 16-bit WAV file to dictate it to the real service")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(data, []byte("data"))
	pcm := data[at+8 : at+8+int(binary.LittleEndian.Uint32(data[at+4:]))]
	options := Options{Language: os.Getenv("TEST_LANGUAGE")}
	options.EndPause, _ = time.ParseDuration(os.Getenv("TEST_END_PAUSE"))
	started := time.Now()
	var last update
	err = Recognize(context.Background(), options, func(ctx context.Context, send func([]byte) error, ready func()) error {
		ready()
		// Trailing silence lets the service close the utterance by itself.
		for _, chunk := range [][]byte{pcm, make([]byte, 32000*4)} {
			for len(chunk) > 0 {
				n := min(1280, len(chunk))
				if err := send(chunk[:n]); err != nil {
					return err
				}
				chunk = chunk[n:]
				time.Sleep(40 * time.Millisecond)
			}
		}
		return nil
	}, func(text string, stable int) {
		last = update{text, stable}
		t.Logf("%6dms stable=%-3d %s", time.Since(started).Milliseconds(), stable, text)
	}, func() {})
	if err != nil || last.text == "" || last.stable != len(last.text) {
		t.Fatalf("err = %v, last = %v", err, last)
	}
}
