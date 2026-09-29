package status

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Host is the machine's own load as the kernel reports it. Outside Linux
// (a developer's laptop) the fields are absent, never invented.
type Host struct {
	CPUs              int
	Load1             *float64
	Load5             *float64
	Load15            *float64
	MemTotalBytes     *int64
	MemAvailableBytes *int64
}

// ReadHost reads /proc. In a container this is the container's view of
// load and the host's view of memory — the numbers an administrator
// still wants next to the queue.
func ReadHost() Host {
	host := Host{CPUs: runtime.NumCPU()}
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		host.Load1, host.Load5, host.Load15 = parseLoadavg(string(raw))
	}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		defer file.Close()
		host.MemTotalBytes, host.MemAvailableBytes = parseMeminfo(bufio.NewScanner(file))
	}
	return host
}

func parseLoadavg(text string) (l1, l5, l15 *float64) {
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return nil, nil, nil
	}
	values := [3]*float64{}
	for i := range values {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil, nil, nil
		}
		values[i] = &v
	}
	return values[0], values[1], values[2]
}

// parseMeminfo reads "MemTotal:  16384 kB" style lines. MemAvailable is
// the kernel's own estimate of what a new workload can use.
func parseMeminfo(scanner *bufio.Scanner) (total, available *int64) {
	for scanner.Scan() {
		name, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok || (name != "MemTotal" && name != "MemAvailable") {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		bytes := kb * 1024
		if name == "MemTotal" {
			total = &bytes
		} else {
			available = &bytes
		}
	}
	return total, available
}
