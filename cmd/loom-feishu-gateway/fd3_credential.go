package main

import (
	"io"
	"os"
)

// readInheritedCredentialFD reads the Lark managed credential from inherited
// descriptor 3 when the parent spawned this gateway with an anonymous
// credential FD. The secret never appears in argv or the normal environment.
func readInheritedCredentialFD() ([]byte, bool) {
	file := os.NewFile(3, "managed-credential")
	if file == nil {
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, false
	}
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}
