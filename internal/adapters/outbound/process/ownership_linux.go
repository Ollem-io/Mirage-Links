//go:build linux

package process

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ownsLoopbackListener proves that the listening socket inode for port belongs
// to an FD held by the launched process group. This distinguishes the child
// from a competitor that wins the reservation-to-exec window.
func ownsLoopbackListener(pgid, port int) (bool, bool, error) {
	inodes := map[string]struct{}{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if err != nil {
			return false, false, fmt.Errorf("read socket table: %w", err)
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			fields := strings.Fields(s.Text())
			if len(fields) < 10 || fields[3] != "0A" { // TCP_LISTEN
				continue
			}
			parts := strings.Split(fields[1], ":")
			if len(parts) != 2 || !procLoopback(parts[0]) {
				continue
			}
			gotPort, err := strconv.ParseInt(parts[1], 16, 32)
			if err == nil && int(gotPort) == port {
				inodes[fields[9]] = struct{}{}
			}
		}
		err = s.Err()
		_ = f.Close()
		if err != nil {
			return false, false, fmt.Errorf("scan socket table: %w", err)
		}
	}
	if len(inodes) == 0 {
		return false, false, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, true, fmt.Errorf("list processes: %w", err)
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || processGroup(pid) != pgid {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil { // process may exit while /proc is traversed
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if _, ok := inodes[inode]; ok {
				return true, true, nil
			}
		}
	}
	return false, true, nil
}

func procLoopback(hexAddress string) bool {
	switch strings.ToUpper(hexAddress) {
	case "0100007F", // IPv4 127.0.0.1
		"00000000000000000000000001000000", // IPv6 ::1
		"0000000000000000FFFF00000100007F": // v4-mapped 127.0.0.1
		return true
	default:
		return false
	}
}

func processGroup(pid int) int {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[0] == "NSpgid:" || fields[0] == "Pgid:") {
			pgid, err := strconv.Atoi(fields[1])
			if err == nil {
				return pgid
			}
		}
	}
	return -1
}
