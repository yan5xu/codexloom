// loom-feishu-gateway connects one Feishu application identity to a
// CodexLoom Connection without requiring lark-cli at runtime.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yan5xu/codex-loom/internal/credentials"
	"github.com/yan5xu/codex-loom/internal/feishu"
	"github.com/yan5xu/codex-loom/internal/feishugw"
	"github.com/yan5xu/codex-loom/internal/hub"
	"github.com/yan5xu/codex-loom/internal/store"
)

func main() {
	hubURL := flag.String("hub", envFirst("CODEX_LOOM_URL", "CHUB_URL", "http://127.0.0.1:4870"), "CodexLoom base URL")
	connectionID := flag.String("connection", envFirst("CODEX_LOOM_CONNECTION_ID", "CHUB_CONNECTION_ID"), "integration connection ID")
	addressID := flag.String("address", envFirst("CODEX_LOOM_ADDRESS_ID", "CHUB_ADDRESS_ID"), "Agent address ID")
	appID := flag.String("app-id", os.Getenv("FEISHU_APP_ID"), "Feishu App ID")
	domainName := flag.String("domain", envFirst("LARK_DOMAIN", "FEISHU_DOMAIN"), "provider domain: lark (Global) or feishu (China)")
	stateFile := flag.String("state-file", "", "gateway state file")
	flag.Parse()
	domain, err := feishu.ParseDomain(*domainName)
	if err != nil {
		log.Fatal(err)
	}

	processProof := gatewayProcessProofFromEnv()
	managedRef, managedRefSet, err := managedRefFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if err := validateGatewayStartup(processProof, managedRefSet, managedRef); err != nil {
		log.Fatal(err)
	}
	secret := ""
	if managedRefSet {
		resolved, err := resolveManagedSecret(dataDir(), credentials.Ref(managedRef))
		if err != nil {
			log.Fatalf("resolve managed credential: %v", err)
		}
		secret = string(resolved)
	} else {
		secret = strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
		if secret == "" {
			if inherited, ok := readInheritedCredentialFD(); ok {
				secret = strings.TrimSpace(string(inherited))
			}
		}
		if secret == "" && strings.TrimSpace(*appID) != "" {
			var err error
			secret, err = feishu.LoadAppSecret(*appID)
			if err != nil {
				log.Fatalf("read Feishu App Secret from keychain: %v", err)
			}
		}
	}
	if *stateFile == "" && *connectionID != "" {
		*stateFile = filepath.Join(dataDir(), "gateway", "feishu-"+*connectionID+".json")
	}
	gateway, err := feishugw.New(feishugw.Config{
		HubURL: *hubURL, ConnectionID: *connectionID, AddressID: *addressID,
		AppID: *appID, AppSecret: secret, Domain: domain, ConnectorToken: os.Getenv("CODEX_LOOM_CONNECTOR_TOKEN"),
		StateFile: *stateFile, ProcessProof: processProof,
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := gateway.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// gatewayProcessProofFromEnv reads the private R1 attempt identity that the
// launch plan froze into the unit environment. All four values must appear
// together and be bounded, secret-free strings; the exact proof is returned to
// the Hub only after the provider socket opens.
func gatewayProcessProofFromEnv() *hub.GatewayProcessHeartbeatParams {
	attemptID := strings.TrimSpace(os.Getenv("CODEX_LOOM_GATEWAY_ATTEMPT_ID"))
	generation := strings.TrimSpace(os.Getenv("CODEX_LOOM_GATEWAY_GENERATION"))
	build := strings.TrimSpace(os.Getenv("CODEX_LOOM_GATEWAY_BUILD"))
	digest := strings.TrimSpace(os.Getenv("CODEX_LOOM_GATEWAY_EXECUTABLE_DIGEST"))
	if attemptID == "" && generation == "" && build == "" && digest == "" {
		return nil
	}
	if attemptID == "" || generation == "" || build == "" || digest == "" {
		log.Fatalf("gateway attempt proof identity is incomplete")
	}
	for _, value := range []string{attemptID, generation, build, digest} {
		if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			log.Fatalf("gateway attempt proof identity is unbounded or invalid")
		}
	}
	return &hub.GatewayProcessHeartbeatParams{
		AttemptID: attemptID, Generation: generation, Build: build, ExecutableDigest: digest,
	}
}

// validateGatewayStartup enforces the startup credential/proof contract before
// any provider socket opens: an explicitly set managed reference must be
// canonical (never falls back to env/FD/Keychain), and a proof-bearing unit
// must run the exact frozen executable. A proof-bearing legacy recovery unit
// legitimately has no managed reference and consumes the legacy source; the
// Hub discriminates recovery proof by its unique recovery generation.
func validateGatewayStartup(proof *hub.GatewayProcessHeartbeatParams, managedRefSet bool, managedRef string) error {
	if managedRefSet && !credentials.IsManagedRef(managedRef) {
		return fmt.Errorf("invalid managed credential reference")
	}
	if proof != nil {
		if err := verifySelfExecutable(proof); err != nil {
			return fmt.Errorf("gateway executable does not match the frozen launch plan: %w", err)
		}
	}
	return nil
}

// managedRefFromEnv distinguishes an unset managed credential reference from an
// explicitly set one. An explicitly set variable must be a canonical managed
// reference: empty, whitespace, or malformed values fail closed before any
// legacy credential source or provider hook runs.
func managedRefFromEnv() (ref string, set bool, err error) {
	raw, set := os.LookupEnv("CODEX_LOOM_MANAGED_CREDENTIAL_REF")
	ref = strings.TrimSpace(raw)
	if !set {
		return "", false, nil
	}
	if !credentials.IsManagedRef(ref) {
		return "", true, fmt.Errorf("invalid managed credential reference")
	}
	return ref, true, nil
}

// verifySelfExecutable proves the running gateway binary is the exact
// executable the launch plan froze: the plan's executable digest must equal the
// sha256 of the current executable file, and the plan's build identity must be
// the digest-derived build used by the maintenance launch entry. A replaced or
// corrupted binary therefore cannot echo the target proof.
func verifySelfExecutable(proof *hub.GatewayProcessHeartbeatParams) error {
	if proof == nil {
		return nil
	}
	current, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve running executable: %w", err)
	}
	return verifyExecutableProof(current, proof)
}

func verifyExecutableProof(path string, proof *hub.GatewayProcessHeartbeatParams) error {
	digest, err := executableDigest(path)
	if err != nil {
		return err
	}
	if proof.ExecutableDigest != digest {
		return fmt.Errorf("running executable digest %s does not match the frozen plan %s", digest, proof.ExecutableDigest)
	}
	expectedBuild := "sha256:" + digest[:16]
	if proof.Build != expectedBuild {
		return fmt.Errorf("running executable build %s does not match the frozen plan %s", expectedBuild, proof.Build)
	}
	return nil
}

func executableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 512<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// resolveManagedSecret opens the C-v1 stable data directory read-only and
// resolves one canonical managed reference. It never writes and never falls
// back to another credential source.
func resolveManagedSecret(dataDir string, ref credentials.Ref) ([]byte, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("managed credential resolution requires a data directory")
	}
	st, err := store.OpenWithOptions(dataDir, store.OpenOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return credentials.ResolveReadOnly(st, ref)
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func dataDir() string {
	if value := envFirst("CODEX_LOOM_DATA", "CODEX_HUB_DATA"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, ".codex-loom")
	if _, err := os.Stat(current); err == nil {
		return current
	}
	return filepath.Join(home, ".codex-hub")
}
