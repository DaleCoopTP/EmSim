package llm

import "testing"

func TestStripThinking(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"no reasoning", "  Мы в Москве.  ", "Мы в Москве."},
		{"closed block", "<think>ума не приложу</think>  Мы в Москве.", "Мы в Москве."},
		{"closed multiline block", "<think>\nшаг 1\nшаг 2\n</think>\n{\"q\":\"yes\"}", `{"q":"yes"}`},
		{"unclosed block cut off by max_tokens", "<think>начинаю рассуждать и обрываюсь", ""},
		{"answer then unclosed block", "Мы в Москве. <think>а может", "Мы в Москве."},
		{"lone closing tag from a template-opened block", "рассуждение без открывающего тега</think>\nМы в Москве.", "Мы в Москве."},
		{"only a closed block", "<think>только рассуждение</think>   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripThinking(tc.in); got != tc.want {
				t.Fatalf("StripThinking(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
