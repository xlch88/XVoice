package ui

import (
	"encoding/binary"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

var playSound = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")

// chime synthesizes a short two-note WAV in memory: rising for start,
// falling for stop. Kept alive in globals because playback is asynchronous.
func chime(notes ...float64) []byte {
	const rate, noteLen = 44100, 0.09
	samples := int(rate * noteLen)
	pcm := make([]byte, 0, len(notes)*samples*2)
	for _, freq := range notes {
		for i := range samples {
			t := float64(i) / rate
			// Quick attack, exponential decay: a soft "ding".
			envelope := math.Min(1, t*200) * math.Exp(-t*28)
			v := 0.35 * envelope * (math.Sin(2*math.Pi*freq*t) + 0.3*math.Sin(4*math.Pi*freq*t))
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(v*32767)))
		}
	}
	wav := []byte("RIFF")
	wav = binary.LittleEndian.AppendUint32(wav, uint32(36+len(pcm)))
	wav = append(wav, "WAVEfmt "...)
	wav = binary.LittleEndian.AppendUint32(wav, 16)
	wav = binary.LittleEndian.AppendUint16(wav, 1) // PCM
	wav = binary.LittleEndian.AppendUint16(wav, 1) // mono
	wav = binary.LittleEndian.AppendUint32(wav, rate)
	wav = binary.LittleEndian.AppendUint32(wav, rate*2)
	wav = binary.LittleEndian.AppendUint16(wav, 2)
	wav = binary.LittleEndian.AppendUint16(wav, 16)
	wav = append(wav, "data"...)
	wav = binary.LittleEndian.AppendUint32(wav, uint32(len(pcm)))
	return append(wav, pcm...)
}

var (
	startSound = chime(880, 1318.5) // A5 → E6
	stopSound  = chime(1318.5, 880)
)

func play(wav []byte) {
	playSound.Call(uintptr(unsafe.Pointer(&wav[0])), 0, 0x4|0x1|0x10) // SND_MEMORY | SND_ASYNC | SND_NODEFAULT
}
