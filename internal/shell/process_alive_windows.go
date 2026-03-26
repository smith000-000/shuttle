//go:build windows

package shell

func processAlive(int) bool {
	return false
}
