//go:build !darwin && !linux

package credentials

import (
	"fmt"
	"os"
)

func verifyOwnerOnlyFile(*os.File) error {
	return fmt.Errorf("managed credentials are unsupported on this platform")
}

func verifyOwnerOnlyPath(string, bool) error {
	return fmt.Errorf("managed credentials are unsupported on this platform")
}

func verifyOwnerOnlyStat(os.FileInfo, bool) error {
	return fmt.Errorf("managed credentials are unsupported on this platform")
}
