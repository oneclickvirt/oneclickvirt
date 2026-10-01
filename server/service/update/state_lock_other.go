//go:build !linux && !darwin

package update

import "fmt"

func lockOperationState(string) (func(), error) {
	return nil, fmt.Errorf("当前系统不支持面板自动升级")
}
