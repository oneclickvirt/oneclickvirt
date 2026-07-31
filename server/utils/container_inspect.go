package utils

import (
	"fmt"
	"strings"
	"unicode"
)

type ContainerInspectRecord struct {
	Name    string
	Status  string
	Image   string
	ID      string
	Created string
}

func ParseContainerInspectOutput(output string) (ContainerInspectRecord, error) {
	var matched *ContainerInspectRecord
	for _, line := range commandOutputLines(output) {
		if isDiagnosticCommandLine(line) || strings.IndexFunc(line, unicode.IsControl) >= 0 {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 4 && len(fields) != 5 {
			continue
		}

		values := make([]string, 4)
		valid := true
		for index := 0; index < len(values); index++ {
			value, err := ParseSingleCommandToken(fields[index])
			if err != nil {
				valid = false
				break
			}
			values[index] = value
		}
		if !valid {
			continue
		}
		created := ""
		if len(fields) == 5 {
			created = strings.TrimSpace(fields[4])
			if strings.IndexFunc(created, unicode.IsControl) >= 0 {
				continue
			}
		}

		record := ContainerInspectRecord{
			Name: values[0], Status: values[1], Image: values[2], ID: values[3], Created: created,
		}
		if matched != nil {
			return ContainerInspectRecord{}, fmt.Errorf("容器inspect输出包含多条有效记录")
		}
		matched = &record
	}
	if matched == nil {
		return ContainerInspectRecord{}, fmt.Errorf("输出中未找到有效的容器inspect记录")
	}
	return *matched, nil
}
