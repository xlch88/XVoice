package session

import (
	"image"
	"testing"
)

func TestFormat(t *testing.T) {
	const chinese = "早上好，今天的日程安排是什么？帮我订一张票。"
	const english = " Good morning. It costs 3.5 dollars, right?"
	for _, c := range []struct{ text, punctuation, want string }{
		{chinese, PunctuationKeep, chinese},
		{chinese, PunctuationNoFinal, "早上好，今天的日程安排是什么帮我订一张票"},
		{chinese, PunctuationNone, "早上好今天的日程安排是什么帮我订一张票"},
		{chinese, PunctuationSpace, "早上好 今天的日程安排是什么 帮我订一张票"},
		{english, PunctuationKeep, "Good morning. It costs 3.5 dollars, right?"},
		{english, PunctuationNoFinal, "Good morning It costs 3.5 dollars, right"},
		{english, PunctuationNone, "Good morning It costs 3.5 dollars right"},
		{english, PunctuationSpace, "Good morning It costs 3.5 dollars right"},
	} {
		if got := Format(c.text, c.punctuation); got != c.want {
			t.Errorf("Format(%q, %q) = %q, want %q", c.text, c.punctuation, got, c.want)
		}
	}
}

// field is an editable text field that counts what was typed into it.
type field struct {
	text   []rune
	erased int
}

func (f *field) Type(erase int, text string) error {
	f.text = append(f.text[:len(f.text)-erase], []rune(text)...)
	f.erased += erase
	return nil
}
func (f *field) Anchor() (image.Rectangle, bool) { return image.Rectangle{}, false }
func (f *field) Done() <-chan struct{}           { return nil }
func (f *field) Reason() string                  { return "" }
func (f *field) Release()                        {}

// A trace of what the service really sends: hypotheses, then the sentence
// revised with punctuation and marked final.
var trace = []struct {
	text   string
	stable int
}{
	{"早", 0},
	{"早上好今天", 0},
	{"早上好，今天的日程安排是什么", len("早上好，今天的日程安排是什么")},
	{"早上好，今天的日程安排是什么帮我", len("早上好，今天的日程安排是什么")},
	{"早上好，今天的日程安排是什么？帮我订票", len("早上好，今天的日程安排是什么？帮我订票")},
	{"早上好，今天的日程安排是什么？帮我订票。", len("早上好，今天的日程安排是什么？帮我订票。")},
}

func TestLiveTypingRevisesTheField(t *testing.T) {
	var f field
	writer := typist{target: &f}
	for _, u := range trace {
		if err := writer.update(u.text, u.stable); err != nil {
			t.Fatal(err)
		}
		if string(f.text) != u.text {
			t.Fatalf("field holds %q after %q", string(f.text), u.text)
		}
	}
	if f.erased == 0 {
		t.Fatal("inserting the comma and question mark must have erased something")
	}
}

func TestConfirmedTypingNeverErases(t *testing.T) {
	for _, punctuation := range []string{PunctuationKeep, PunctuationNoFinal, PunctuationNone, PunctuationSpace} {
		var f field
		writer := typist{target: &f, confirmed: true, punctuation: punctuation}
		for _, u := range trace {
			if err := writer.update(u.text, u.stable); err != nil {
				t.Fatal(err)
			}
			if want := Format(u.text[:u.stable], punctuation); string(f.text) != want {
				t.Fatalf("%q: field holds %q, want %q", punctuation, string(f.text), want)
			}
		}
		if f.erased != 0 {
			t.Errorf("%q: erased %d characters of confirmed text", punctuation, f.erased)
		}
	}
}
