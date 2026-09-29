package status

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15 := parseLoadavg("0.52 1.25 3.00 2/611 18422\n")
	if l1 == nil || l5 == nil || l15 == nil || *l1 != 0.52 || *l5 != 1.25 || *l15 != 3.0 {
		t.Fatalf("loadavg = %v %v %v", l1, l5, l15)
	}
	for _, bad := range []string{"", "0.5 1.0", "a b c"} {
		if a, b, c := parseLoadavg(bad); a != nil || b != nil || c != nil {
			t.Fatalf("%q parsed as %v %v %v", bad, a, b, c)
		}
	}
}

func TestParseMeminfo(t *testing.T) {
	text := "MemTotal:       16384000 kB\nMemFree:          100000 kB\nMemAvailable:    8192000 kB\nSwapTotal:       0 kB\n"
	total, available := parseMeminfo(bufio.NewScanner(strings.NewReader(text)))
	if total == nil || available == nil || *total != 16384000*1024 || *available != 8192000*1024 {
		t.Fatalf("meminfo = %v %v", total, available)
	}
	if total, available := parseMeminfo(bufio.NewScanner(strings.NewReader("Cached: 5 kB\n"))); total != nil || available != nil {
		t.Fatalf("missing keys parsed as %v %v", total, available)
	}
}
