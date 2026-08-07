//go:build unix

package httpapi

import (
	"errors"
	"html"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) prepareManagedGatewayRestart(connection hub.PlatformConnection) (managedGatewayRestartPlan, error) {
	provider := managedGatewayProvider(connection.Provider)
	service, err := migrationGatewayServiceFor(provider, connection.ID)
	if err != nil {
		return managedGatewayRestartPlan{}, err
	}
	unit, err := readBoundedPrivateFile(service.UnitPath, 1<<20, false)
	if err != nil {
		return managedGatewayRestartPlan{}, err
	}
	arguments, err := gatewayUnitArguments(string(unit), service.Manager)
	if err != nil {
		return managedGatewayRestartPlan{}, err
	}
	wrapperName := "loom-" + provider + "-gateway"
	if provider == "feishu" {
		wrapperName = "loom-feishu-gateway"
	}
	wrapperPath := findArgumentByBase(arguments, wrapperName)
	if wrapperPath == "" {
		return managedGatewayRestartPlan{}, errors.New("gateway wrapper path is missing from the unit")
	}
	observed, err := buildinfo.ObserveExecutable(wrapperPath)
	if err != nil {
		return managedGatewayRestartPlan{}, err
	}
	build := strings.TrimSpace(s.build.Commit)
	if !buildinfo.ValidBuildIdentity(build) {
		return managedGatewayRestartPlan{}, errors.New("gateway build identity is unavailable")
	}
	previousGeneration := findArgumentValue(arguments, "--generation")
	if !validOptionalGatewayGeneration(previousGeneration) {
		return managedGatewayRestartPlan{}, errors.New("previous gateway generation is invalid")
	}
	targetGeneration := migrationGatewayGeneration("restart/"+connection.ID+"/"+previousGeneration+"/"+time.Now().UTC().Format(time.RFC3339Nano), build)
	targetUnit, err := setGatewayUnitGeneration(string(unit), service.Manager, previousGeneration, targetGeneration)
	if err != nil {
		return managedGatewayRestartPlan{}, err
	}
	base := hub.CredentialMigrationGatewayReceipt{
		Manager: service.Manager, Service: service.Service, Build: build, ExecutableSHA256: observed.SHA256,
	}
	previous, target := base, base
	previous.Status, previous.Generation = "restart_recovery_prepared", previousGeneration
	target.Status, target.Generation = "restart_prepared", targetGeneration
	return managedGatewayRestartPlan{
		Applicable: true, UnitPath: service.UnitPath, OriginalUnit: append([]byte(nil), unit...), TargetUnit: []byte(targetUnit),
		Previous: previous, Target: target,
	}, nil
}

func setGatewayUnitGeneration(unit, manager, previous, target string) (string, error) {
	if previous != "" {
		updated := replaceUnitArgument(unit, previous, target, manager)
		if updated == unit {
			return "", errors.New("gateway generation argument could not be replaced")
		}
		return updated, nil
	}
	switch manager {
	case "launchd":
		key := strings.Index(unit, "<key>ProgramArguments</key>")
		if key < 0 {
			return "", errors.New("gateway unit has no ProgramArguments")
		}
		closeOffset := strings.Index(unit[key:], "</array>")
		if closeOffset < 0 {
			return "", errors.New("gateway ProgramArguments array is incomplete")
		}
		closeIndex := key + closeOffset
		addition := "      <string>--generation</string>\n      <string>" + html.EscapeString(target) + "</string>\n"
		return unit[:closeIndex] + addition + unit[closeIndex:], nil
	case "systemd":
		lines := strings.Split(unit, "\n")
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "ExecStart=") {
				lines[index] = line + " " + systemdQuote("--generation") + " " + systemdQuote(target)
				return strings.Join(lines, "\n"), nil
			}
		}
		return "", errors.New("gateway unit has no ExecStart")
	default:
		return "", errors.New("unsupported gateway manager")
	}
}

func writeManagedGatewayRestartUnit(path string, payload []byte) error {
	return writeSyncedPrivateFile(path, payload, 0o600)
}
