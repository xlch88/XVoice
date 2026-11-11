package session

import (
	"strings"

	"xvoice/internal/config"
	"xvoice/internal/desktop"
)

const (
	TypingLive      = ""          // Type hypotheses as they arrive and revise them.
	TypingConfirmed = "confirmed" // Type a sentence only once it is final.

	PunctuationKeep    = ""
	PunctuationNoFinal = "no-final"
	PunctuationNone    = "none"
	PunctuationSpace   = "space"
)

var Typings = []config.Choice{
	{Value: TypingLive, Name: "边说边上屏（会回改）"},
	{Value: TypingConfirmed, Name: "整句确认后上屏"},
}

// Punctuations mirror the official client's "标点设置".
var Punctuations = []config.Choice{
	{Value: PunctuationKeep, Name: "智能添加标点"},
	{Value: PunctuationNoFinal, Name: "省略句末标点"},
	{Value: PunctuationNone, Name: "省略所有标点"},
	{Value: PunctuationSpace, Name: "空格代替标点"},
}

// AutoStops are asr.Options.EndPause durations.
var AutoStops = []config.Choice{
	{Value: "", Name: "不自动结束（连续听写）"},
	{Value: "1s", Name: "停顿 1 秒后结束"},
	{Value: "1.5s", Name: "停顿 1.5 秒后结束"},
	{Value: "2s", Name: "停顿 2 秒后结束"},
	{Value: "3s", Name: "停顿 3 秒后结束"},
	{Value: "5s", Name: "停顿 5 秒后结束"},
}

const (
	sentenceEnders = "。！？!?"
	otherMarks     = "，、；：,"
)

// Format applies the punctuation setting to a transcript. Like the official
// client it is done locally on the recognized text; the service always
// punctuates. A "." only counts as punctuation at the end of a sentence, so
// decimals and domain names survive.
func Format(text, punctuation string) string {
	// English models start their output with a space.
	text = strings.TrimLeft(text, " ")
	if punctuation == PunctuationKeep {
		return text
	}
	runes := []rune(text)
	var out strings.Builder
	for i, r := range runes {
		mark := strings.ContainsRune(sentenceEnders, r) ||
			r == '.' && (i == len(runes)-1 || runes[i+1] == ' ') ||
			punctuation != PunctuationNoFinal && strings.ContainsRune(otherMarks, r)
		switch {
		case !mark:
			out.WriteRune(r)
		case punctuation == PunctuationSpace:
			out.WriteRune(' ')
		}
	}
	if punctuation != PunctuationSpace {
		return out.String()
	}
	// "Hello, world." became "Hello  world ": one space per gap, none trailing.
	return strings.Join(strings.Fields(out.String()), " ")
}

// typist keeps the external text field equal to the formatted transcript by
// erasing back to the first difference and typing the rest. In confirmed
// mode only final text is typed, so it never erases.
type typist struct {
	target      desktop.Target
	confirmed   bool
	punctuation string
	typed       []rune
}

func (t *typist) update(text string, stable int) error {
	if t.confirmed {
		text = text[:stable]
	}
	next := []rune(Format(text, t.punctuation))
	same := 0
	for same < len(t.typed) && same < len(next) && t.typed[same] == next[same] {
		same++
	}
	if same == len(t.typed) && same == len(next) {
		return nil
	}
	if err := t.target.Type(len(t.typed)-same, string(next[same:])); err != nil {
		return err
	}
	t.typed = next
	return nil
}
