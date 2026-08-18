// Package launchagent renders and preflights CodexLoom's managed macOS
// LaunchAgent without loading or restarting the job.
package launchagent

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yan5xu/codex-loom/internal/proxyenv"
)

const (
	Label        = "com.pinix.codex-loom"
	DefaultLog   = "/tmp/codex-loom.log"
	maxPlistSize = 1 << 20
)

type Config struct {
	Executable       string
	WorkingDirectory string
	Path             string
	NoProxy          string
	StandardOutPath  string
	StandardErrPath  string
}

type Inspection struct {
	Label            string           `json:"label"`
	Executable       string           `json:"executable"`
	WorkingDirectory string           `json:"workingDirectory"`
	Proxy            proxyenv.Summary `json:"proxy"`
}

func Render(config Config) ([]byte, Inspection, error) {
	config.Executable = filepath.Clean(strings.TrimSpace(config.Executable))
	config.WorkingDirectory = filepath.Clean(strings.TrimSpace(config.WorkingDirectory))
	config.Path = strings.TrimSpace(config.Path)
	if config.StandardOutPath == "" {
		config.StandardOutPath = DefaultLog
	}
	if config.StandardErrPath == "" {
		config.StandardErrPath = DefaultLog
	}
	if err := validatePathConfig(config); err != nil {
		return nil, Inspection{}, err
	}
	canonical, err := proxyenv.Normalize(config.NoProxy)
	if err != nil {
		return nil, Inspection{}, err
	}

	var body bytes.Buffer
	write := func(text string) { _, _ = body.WriteString(text) }
	value := func(text string) {
		_ = xml.EscapeText(&body, []byte(text))
	}
	write(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>`)
	value(Label)
	write(`</string>
  <key>ProgramArguments</key>
  <array>
    <string>`)
	value(config.Executable)
	write(`</string>
  </array>
  <key>WorkingDirectory</key>
  <string>`)
	value(config.WorkingDirectory)
	write(`</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>`)
	value(config.StandardOutPath)
	write(`</string>
  <key>StandardErrorPath</key>
  <string>`)
	value(config.StandardErrPath)
	write(`</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>`)
	value(config.Path)
	write(`</string>
    <key>`)
	value(proxyenv.ManagedVariable)
	write(`</key>
    <string>`)
	value(canonical)
	write(`</string>
  </dict>
</dict>
</plist>
`)

	inspection := Inspection{
		Label: Label, Executable: config.Executable, WorkingDirectory: config.WorkingDirectory,
		Proxy: proxyenv.Summarize(canonical),
	}
	return body.Bytes(), inspection, nil
}

func validatePathConfig(config Config) error {
	if !filepath.IsAbs(config.Executable) {
		return fmt.Errorf("LaunchAgent executable must be an absolute path")
	}
	info, err := os.Stat(config.Executable)
	if err != nil {
		return fmt.Errorf("inspect LaunchAgent executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("LaunchAgent executable must be an executable regular file")
	}
	if !filepath.IsAbs(config.WorkingDirectory) {
		return fmt.Errorf("LaunchAgent working directory must be an absolute path")
	}
	info, err = os.Stat(config.WorkingDirectory)
	if err != nil {
		return fmt.Errorf("inspect LaunchAgent working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("LaunchAgent working directory must be a directory")
	}
	dir, err := os.Open(config.WorkingDirectory)
	if err != nil {
		return fmt.Errorf("open LaunchAgent working directory: %w", err)
	}
	_ = dir.Close()
	if config.Path == "" {
		return fmt.Errorf("LaunchAgent PATH must not be empty")
	}
	for _, logPath := range []string{config.StandardOutPath, config.StandardErrPath} {
		if !filepath.IsAbs(logPath) {
			return fmt.Errorf("LaunchAgent log paths must be absolute")
		}
	}
	return nil
}

// Write atomically installs a rendered plist with owner-only permissions. It
// does not invoke launchctl or otherwise change process state.
func Write(path string, data []byte) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return fmt.Errorf("LaunchAgent plist path must be absolute")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace a symlinked LaunchAgent plist")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect LaunchAgent plist: %w", err)
	}
	parent := filepath.Dir(path)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("inspect LaunchAgent directory: %w", err)
		}
		return fmt.Errorf("LaunchAgent parent path is not a directory")
	}
	temporary, err := os.CreateTemp(parent, ".codex-loom-launch-agent-*")
	if err != nil {
		return fmt.Errorf("create temporary LaunchAgent plist: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return fmt.Errorf("write LaunchAgent plist: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close LaunchAgent plist: %w", closeErr)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace LaunchAgent plist: %w", err)
	}
	return nil
}

func InspectFile(path string) (Inspection, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return Inspection{}, fmt.Errorf("LaunchAgent plist path must be absolute")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("read LaunchAgent plist: %w", err)
	}
	return Inspect(data)
}

func Inspect(data []byte) (Inspection, error) {
	if len(data) == 0 || len(data) > maxPlistSize {
		return Inspection{}, fmt.Errorf("LaunchAgent plist size is invalid")
	}
	record, err := decodePlist(data)
	if err != nil {
		return Inspection{}, fmt.Errorf("decode LaunchAgent plist: %w", err)
	}
	inspection, environment, err := inspectBase(record)
	if err != nil {
		return Inspection{}, err
	}
	managed, managedPresent := environment[proxyenv.ManagedVariable]
	if !managedPresent {
		return Inspection{}, fmt.Errorf("LaunchAgent is missing %s", proxyenv.ManagedVariable)
	}
	upper := environment["NO_PROXY"]
	lower := environment["no_proxy"]
	canonical, err := proxyenv.Normalize(upper, lower, managed)
	if err != nil {
		return Inspection{}, err
	}
	if managed != canonical {
		return Inspection{}, fmt.Errorf("LaunchAgent %s is not the canonical merged proxy bypass", proxyenv.ManagedVariable)
	}
	inspection.Proxy = proxyenv.Summarize(canonical)
	return inspection, nil
}

// UpdateProxy preserves an existing managed plist and every unrelated
// EnvironmentVariables entry while collapsing the three explicit proxy
// spellings into the canonical managed variable. It never loads the unit.
func UpdateProxy(data []byte, explicit string) ([]byte, Inspection, error) {
	if len(data) == 0 || len(data) > maxPlistSize {
		return nil, Inspection{}, fmt.Errorf("LaunchAgent plist size is invalid")
	}
	record, err := decodePlist(data)
	if err != nil {
		return nil, Inspection{}, fmt.Errorf("decode LaunchAgent plist: %w", err)
	}
	_, environment, err := inspectBase(record)
	if err != nil {
		return nil, Inspection{}, err
	}
	canonical, err := proxyenv.Normalize(
		environment["NO_PROXY"], environment["no_proxy"], environment[proxyenv.ManagedVariable], explicit,
	)
	if err != nil {
		return nil, Inspection{}, err
	}
	delete(environment, "NO_PROXY")
	delete(environment, "no_proxy")
	environment[proxyenv.ManagedVariable] = canonical
	start, end, err := environmentDictionaryBounds(data)
	if err != nil {
		return nil, Inspection{}, err
	}
	replacement := renderEnvironment(environment)
	updated := make([]byte, 0, len(data)-(end-start)+len(replacement))
	updated = append(updated, data[:start]...)
	updated = append(updated, replacement...)
	updated = append(updated, data[end:]...)
	inspection, err := Inspect(updated)
	if err != nil {
		return nil, Inspection{}, fmt.Errorf("validate updated LaunchAgent plist: %w", err)
	}
	return updated, inspection, nil
}

func inspectBase(record map[string]any) (Inspection, map[string]string, error) {
	label, _ := record["Label"].(string)
	if label != Label {
		return Inspection{}, nil, fmt.Errorf("LaunchAgent label must be %s", Label)
	}
	arguments, _ := record["ProgramArguments"].([]any)
	if len(arguments) == 0 {
		return Inspection{}, nil, fmt.Errorf("LaunchAgent ProgramArguments is empty")
	}
	executable, _ := arguments[0].(string)
	workingDirectory, _ := record["WorkingDirectory"].(string)
	rawEnvironment, ok := record["EnvironmentVariables"].(map[string]any)
	if !ok {
		return Inspection{}, nil, fmt.Errorf("LaunchAgent EnvironmentVariables must be a dictionary")
	}
	environment := make(map[string]string, len(rawEnvironment))
	for key, raw := range rawEnvironment {
		value, ok := raw.(string)
		if !ok {
			return Inspection{}, nil, fmt.Errorf("LaunchAgent environment values must be strings")
		}
		environment[key] = value
	}
	pathValue := environment["PATH"]
	standardOut, _ := record["StandardOutPath"].(string)
	standardErr, _ := record["StandardErrorPath"].(string)
	if err := validatePathConfig(Config{
		Executable: executable, WorkingDirectory: workingDirectory,
		Path: pathValue, StandardOutPath: standardOut, StandardErrPath: standardErr,
	}); err != nil {
		return Inspection{}, nil, err
	}
	return Inspection{Label: label, Executable: executable, WorkingDirectory: workingDirectory}, environment, nil
}

func environmentDictionaryBounds(data []byte) (int, int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	wantDictionary := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return 0, 0, fmt.Errorf("locate LaunchAgent EnvironmentVariables: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "key" {
			var key string
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return 0, 0, err
			}
			wantDictionary = key == "EnvironmentVariables"
			continue
		}
		if !wantDictionary {
			continue
		}
		if start.Name.Local != "dict" {
			return 0, 0, fmt.Errorf("LaunchAgent EnvironmentVariables must be a dictionary")
		}
		contentStart := int(decoder.InputOffset())
		depth := 1
		for depth > 0 {
			nested, err := decoder.Token()
			if err != nil {
				return 0, 0, err
			}
			switch typed := nested.(type) {
			case xml.StartElement:
				if typed.Name.Local == "dict" {
					depth++
				}
			case xml.EndElement:
				if typed.Name.Local == "dict" {
					depth--
					if depth == 0 {
						after := int(decoder.InputOffset())
						closing := bytes.LastIndex(data[:after], []byte("</dict>"))
						if closing < contentStart {
							return 0, 0, fmt.Errorf("locate LaunchAgent EnvironmentVariables closing tag")
						}
						return contentStart, closing, nil
					}
				}
			}
		}
	}
}

func renderEnvironment(environment map[string]string) []byte {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var body bytes.Buffer
	body.WriteByte('\n')
	for _, key := range keys {
		body.WriteString("    <key>")
		_ = xml.EscapeText(&body, []byte(key))
		body.WriteString("</key>\n    <string>")
		_ = xml.EscapeText(&body, []byte(environment[key]))
		body.WriteString("</string>\n")
	}
	body.WriteString("  ")
	return body.Bytes()
}

func decodePlist(data []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("missing plist dictionary")
			}
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "dict" {
			continue
		}
		value, err := decodeDict(decoder, start)
		if err != nil {
			return nil, err
		}
		return value, nil
	}
}

func decodeDict(decoder *xml.Decoder, start xml.StartElement) (map[string]any, error) {
	record := map[string]any{}
	var key string
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if typed.Name.Local == "key" {
				if err := decoder.DecodeElement(&key, &typed); err != nil {
					return nil, err
				}
				continue
			}
			if key == "" {
				return nil, fmt.Errorf("plist value has no key")
			}
			value, err := decodeValue(decoder, typed)
			if err != nil {
				return nil, err
			}
			if _, exists := record[key]; exists {
				return nil, fmt.Errorf("plist dictionary contains a duplicate key")
			}
			record[key] = value
			key = ""
		case xml.EndElement:
			if typed.Name == start.Name {
				return record, nil
			}
		}
	}
}

func decodeValue(decoder *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "string", "integer", "real", "date", "data":
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return nil, err
		}
		return value, nil
	case "true", "false":
		if err := decoder.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	case "dict":
		return decodeDict(decoder, start)
	case "array":
		var values []any
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			switch typed := token.(type) {
			case xml.StartElement:
				value, err := decodeValue(decoder, typed)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			case xml.EndElement:
				if typed.Name == start.Name {
					return values, nil
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported plist value %s", start.Name.Local)
	}
}
