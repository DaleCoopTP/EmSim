package training

import (
	"encoding/json"
	"testing"

	"emsim/internal/content"
)

func TestMayCloseItem(t *testing.T) {
	terminal := Item{Workflow: content.Workflow{Terminal: []content.Reaction{content.ReactionCompleted, content.ReactionRefused}}}
	pilot := Item{}
	status := func(s string) Command {
		raw, _ := json.Marshal(map[string]string{"status": s})
		return Command{Type: CommandSetStatus, Payload: raw}
	}
	cases := []struct {
		name string
		item Item
		cmd  Command
		want bool
	}{
		{"explicit close", pilot, Command{Type: CommandClose}, true},
		{"terminal status", terminal, status("completed"), true},
		{"non-terminal status", terminal, status("arrived"), false},
		{"pilot workflow status", pilot, status("completed"), false},
		{"malformed payload", terminal, Command{Type: CommandSetStatus, Payload: json.RawMessage(`{`)}, false},
		{"comment", terminal, Command{Type: CommandAddComment}, false},
	}
	for _, tc := range cases {
		if got := mayCloseItem(tc.item, tc.cmd); got != tc.want {
			t.Errorf("%s: mayCloseItem = %v, want %v", tc.name, got, tc.want)
		}
	}
}
