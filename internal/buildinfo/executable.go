package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// ExecutableEvidence is non-secret process identity that a managed child can
// report in its heartbeat. Build is self-reported by the running binary while
// SHA256 is observed from the executable file that launched it.
type ExecutableEvidence struct {
	Build  string `json:"build,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// CurrentExecutableEvidence observes the currently running executable. It
// intentionally does not expose the executable path through public wire data.
func CurrentExecutableEvidence() (ExecutableEvidence, error) {
	path, err := os.Executable()
	if err != nil {
		return ExecutableEvidence{}, fmt.Errorf("resolve current executable: %w", err)
	}
	evidence, err := ObserveExecutable(path)
	if err != nil {
		return ExecutableEvidence{}, err
	}
	if build := strings.TrimSpace(Commit); ValidBuildIdentity(build) {
		evidence.Build = build
	}
	return evidence, nil
}

// ObserveExecutable returns the digest of a regular executable candidate.
// Callers supply the expected build separately because reading linker metadata
// from an arbitrary sibling binary is platform-specific and not authoritative.
func ObserveExecutable(path string) (ExecutableEvidence, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return ExecutableEvidence{}, fmt.Errorf("open executable: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ExecutableEvidence{}, fmt.Errorf("inspect executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ExecutableEvidence{}, fmt.Errorf("executable is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ExecutableEvidence{}, fmt.Errorf("digest executable: %w", err)
	}
	return ExecutableEvidence{SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// ValidExecutableSHA256 validates the canonical lowercase digest used by
// heartbeat and migration evidence.
func ValidExecutableSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ValidBuildIdentity rejects placeholders and control characters so a build
// can be used as evidence rather than merely as display metadata.
func ValidBuildIdentity(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.EqualFold(value, "unknown") || strings.EqualFold(value, "dev") {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
