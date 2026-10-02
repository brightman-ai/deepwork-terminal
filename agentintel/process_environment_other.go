//go:build !linux && !darwin

package agentintel

import "errors"

func processEnvironment(int) ([]string, error) {
	return nil, errors.New("process environment inspection is unsupported on this platform")
}
