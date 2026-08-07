// Package credentialpipe starts a provider adapter with a one-shot credential
// payload on an anonymous inherited file descriptor. Secret values never enter
// argv, the ordinary child environment, stdout, or stderr.
package credentialpipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const childCredentialFD = 3

// Run starts executable, appending --credential-fd 3 to arguments. The child
// receives one JSON value and EOF. inheritedSecretNames are removed from the
// child environment even when the wrapper inherited them from an old unit.
func Run(ctx context.Context, executable string, arguments []string, payload any, inheritedSecretNames ...string) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode credential payload")
	}
	defer clear(encoded)

	reader, writer, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create credential pipe")
	}
	defer reader.Close()
	defer writer.Close()

	childArguments := append(append([]string(nil), arguments...), "--credential-fd", fmt.Sprint(childCredentialFD))
	command := exec.Command(executable, childArguments...)
	command.ExtraFiles = []*os.File{reader}
	command.Env = withoutEnvironment(os.Environ(), inheritedSecretNames)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start provider adapter")
	}
	_ = reader.Close()
	if err := writeAll(writer, encoded); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("write provider credential pipe")
	}
	if err := writer.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("close provider credential pipe")
	}

	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			return fmt.Errorf("provider adapter exited: %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = command.Process.Signal(os.Interrupt)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case err := <-wait:
			if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("provider adapter stopped: %w", err)
			}
			return nil
		case <-timer.C:
			_ = command.Process.Kill()
			<-wait
			return nil
		}
	}
}

func writeAll(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return errors.New("credential pipe made no write progress")
		}
		data = data[written:]
	}
	return nil
}

func withoutEnvironment(environment, names []string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			blocked[strings.ToUpper(name)] = struct{}{}
		}
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[strings.ToUpper(name)]; !remove {
			result = append(result, entry)
		}
	}
	return result
}

func clear(data []byte) {
	for index := range data {
		data[index] = 0
	}
}
