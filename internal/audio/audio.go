// Package audio captures 16 kHz mono 16-bit PCM from a selectable microphone.
// Each platform provides Devices and Capture in its own build-tagged file.
package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
)

type Device struct {
	ID      string
	Name    string
	Default bool
}

var ErrUnsupported = errors.New("当前平台尚未实现麦克风采集")

// Level maps a PCM chunk's RMS to 0..1 on a rough dB scale for meters.
func Level(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		sum += v * v
	}
	rms := math.Sqrt(sum/float64(len(pcm)/2)) / 32768
	if rms <= 0 {
		return 0
	}
	// -60 dB .. 0 dB
	return math.Max(0, math.Min(1, 1+20*math.Log10(rms)/60))
}

// queue decouples the device thread from a possibly stalled network sender.
// Overflow is reported instead of silently dropping speech.
func pump(ctx context.Context, send func([]byte) error, record func(ctx context.Context, chunks chan<- []byte) error) error {
	chunks := make(chan []byte, 500) // ~20 s of 40 ms chunks, covers ASR session rotation.
	failed := make(chan error, 1)
	recordCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		failed <- record(recordCtx, chunks)
	}()
	defer func() {
		stop()
		<-done
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-failed:
			return err
		case chunk := <-chunks:
			if ctx.Err() != nil {
				return nil
			}
			if err := send(chunk); err != nil {
				return err
			}
		}
	}
}
