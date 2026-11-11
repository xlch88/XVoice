//go:build !windows

package audio

import "context"

func Devices() ([]Device, error) { return nil, ErrUnsupported }

func Capture(ctx context.Context, deviceID string, send func([]byte) error, ready func()) error {
	return ErrUnsupported
}
