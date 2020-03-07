// Package mount was forked from https://github.com/opencontainers/runc/tree/fc5759cf4fcf3f9c77c5973a24d37188dbcc92ee/libcontainer/mount
package mount

// GetMounts retrieves a list of mounts for the current running process.
func GetMounts() ([]*Info, error) {
	return parseMountTable()
}
