// Package session runs one dictation at a time: lock the focused field, stream
// the microphone to the recognizer and keep the field in sync with the
// transcript. It is platform independent; the platform parts come in through
// Controller.
package session

import (
	"context"
	"image"
	"sync"
	"time"

	"xvoice/internal/asr"
	"xvoice/internal/audio"
	"xvoice/internal/config"
	"xvoice/internal/desktop"
)

type Phase int

const (
	Idle Phase = iota
	Connecting
	Listening
	Finishing // The microphone is closed; the last result is still coming.
)

type State struct {
	Phase   Phase
	Message string // For Idle: why the last dictation ended.
	Text    string // Raw transcript of the current dictation, unconfirmed words included.
	Level   float64
	Anchor  image.Rectangle // Where the text goes, for placing the floating bar.
	Placed  bool            // Anchor is meaningful.
}

type Controller struct {
	Acquire func() (desktop.Target, error)
	Config  func() config.Config // Read at the start of each dictation.
	OnState func(State)          // Called from arbitrary goroutines.

	mu       sync.Mutex
	abort    context.CancelFunc // Stop typing at once.
	finish   context.CancelFunc // Close the microphone, still type the last result.
	finished chan struct{}
}

// Toggle starts a dictation, or ends the running one gracefully.
func (c *Controller) Toggle() {
	c.mu.Lock()
	finish := c.finish
	c.mu.Unlock()
	if finish != nil {
		finish()
	} else {
		c.Start()
	}
}

// Stop ends the running dictation without waiting for pending results.
func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.abort != nil {
		c.abort()
	}
}

func (c *Controller) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.abort != nil {
		return
	}
	// The previous capture must have released the microphone.
	if c.finished != nil {
		<-c.finished
	}
	target, err := c.Acquire()
	if err != nil {
		c.OnState(State{Phase: Idle, Message: err.Error()})
		return
	}
	ctx, abort := context.WithCancel(context.Background())
	listening, finish := context.WithCancel(ctx)
	done := make(chan struct{})
	c.abort, c.finish, c.finished = abort, finish, done
	go func() {
		defer close(done)
		message := c.run(ctx, listening, target)
		target.Release()
		abort()
		c.mu.Lock()
		c.abort, c.finish = nil, nil
		c.mu.Unlock()
		c.OnState(State{Phase: Idle, Message: message})
	}()
}

// run dictates until ctx ends (abort) or listening ends and the recognizer has
// delivered its last result. It returns why the dictation ended.
func (c *Controller) run(ctx, listening context.Context, target desktop.Target) string {
	cfg := c.Config()
	anchor, placed := target.Anchor()
	var mu sync.Mutex
	state := State{Phase: Connecting, Anchor: anchor, Placed: placed}
	publish := func(change func(*State)) {
		mu.Lock()
		change(&state)
		s := state
		mu.Unlock()
		c.OnState(s)
	}
	publish(func(*State) {})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-target.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	go func() {
		select {
		case <-listening.Done():
			if ctx.Err() == nil {
				publish(func(s *State) { s.Phase, s.Level = Finishing, 0 })
			}
		case <-ctx.Done():
		}
	}()
	options := asr.Options{Language: cfg.Language, Domain: cfg.Domain}
	options.EndPause, _ = time.ParseDuration(cfg.AutoStop)
	writer := typist{target: target, confirmed: cfg.Typing == TypingConfirmed, punctuation: cfg.Punctuation}
	var typeErr error
	err := asr.Recognize(ctx, options, func(_ context.Context, send func([]byte) error, ready func()) error {
		// Capture follows listening, not ctx: ending it alone lets the
		// recognizer fetch the final result.
		return audio.Capture(listening, cfg.Microphone, func(pcm []byte) error {
			level := audio.Level(pcm)
			publish(func(s *State) { s.Level = level })
			return send(pcm)
		}, ready)
	}, func(text string, stable int) {
		publish(func(s *State) { s.Text = text })
		if typeErr == nil {
			if typeErr = writer.update(text, stable); typeErr != nil {
				cancel()
			}
		}
	}, func() {
		publish(func(s *State) { s.Phase = Listening })
	})
	select {
	case <-target.Done():
		return target.Reason()
	default:
	}
	switch {
	case typeErr != nil:
		return typeErr.Error()
	case err == asr.ErrSilence:
		return err.Error()
	case err != nil:
		return "识别中断：" + err.Error()
	case ctx.Err() != nil:
		return "已停止"
	}
	return "听写完成"
}
