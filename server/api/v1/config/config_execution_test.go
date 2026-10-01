package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestConfigurationLogsDrainAndPanicBecomesFailure(t *testing.T) {
	var lines []string
	err := runConfigurationWithLogs(func(log chan string) error {
		for i := 0; i < 500; i++ {
			log <- fmt.Sprint(i)
		}
		panic("fixture failure")
	}, func(line string) { lines = append(lines, line) })
	if err == nil || !strings.Contains(err.Error(), "fixture failure") {
		t.Fatalf("panic result: %v", err)
	}
	if len(lines) != 500 || lines[0] != "0" || lines[499] != "499" {
		t.Fatalf("lost or reordered logs: %d", len(lines))
	}
}
