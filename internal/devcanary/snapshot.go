// Package devcanary creates isolated, read-only data projections for local UI
// and API verification. It never copies runtime event logs or credentials.
package devcanary

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var jsonFiles = []string{
	"agents.json", "sessions.json", "profiles.json", "team-links.json",
	"organization-links.json", "integrations.json", "schedules.json", "remote.json",
}

var ndjsonFiles = []string{
	"comms.ndjson", "messages.ndjson", "inbox.ndjson", "attempts.ndjson",
	"outbox.ndjson", "provider-operations.ndjson", "human-requests.ndjson",
}

type Options struct {
	Agents []string
}

type Summary struct {
	AgentIDs    []string `json:"agentIds"`
	AgentNames  []string `json:"agentNames"`
	FilesCopied int      `json:"filesCopied"`
	Filtered    bool     `json:"filtered"`
}

type agentRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type addressRecord struct {
	ID           string `json:"id"`
	AgentID      string `json:"agentId"`
	ConnectionID string `json:"connectionId"`
}

type membershipRecord struct {
	ID        string `json:"id"`
	AddressID string `json:"addressId"`
}

// CreateSnapshot copies durable projections into destination. With no Agent
// filter it copies all projections. With filters it retains only records tied
// to the selected Agents and their Connector identities.
func CreateSnapshot(source, destination string, options Options) (Summary, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return Summary{}, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return Summary{}, err
	}
	if source == destination {
		return Summary{}, fmt.Errorf("canary destination must differ from the source data directory")
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return Summary{}, err
	}
	if err := os.MkdirAll(filepath.Join(destination, "events"), 0o700); err != nil {
		return Summary{}, err
	}

	agents, err := loadAgents(filepath.Join(source, "agents.json"))
	if err != nil {
		return Summary{}, err
	}
	selected, err := selectAgents(agents, options.Agents)
	if err != nil {
		return Summary{}, err
	}
	summary := summarize(selected, len(options.Agents) > 0)

	if len(options.Agents) == 0 {
		for _, name := range append(append([]string{}, jsonFiles...), ndjsonFiles...) {
			copied, err := copyProjection(filepath.Join(source, name), filepath.Join(destination, name), strings.HasSuffix(name, ".ndjson"))
			if err != nil {
				return Summary{}, err
			}
			if copied {
				summary.FilesCopied++
			}
		}
	} else if err := createFilteredSnapshot(source, destination, selected, &summary); err != nil {
		return Summary{}, err
	}

	attachments := filepath.Join(source, "attachments")
	if info, err := os.Stat(attachments); err == nil && info.IsDir() {
		_ = os.Symlink(attachments, filepath.Join(destination, "attachments"))
	}
	return summary, nil
}

func createFilteredSnapshot(source, destination string, agents map[string]json.RawMessage, summary *Summary) error {
	selected := map[string]struct{}{}
	for id, raw := range agents {
		selected[id] = struct{}{}
		var agent agentRecord
		_ = json.Unmarshal(raw, &agent)
		selected[agent.ID] = struct{}{}
		selected[agent.Name] = struct{}{}
	}
	if err := writeJSON(filepath.Join(destination, "agents.json"), agents); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(destination, "sessions.json"), agents); err != nil {
		return err
	}
	summary.FilesCopied += 2

	for _, name := range []string{"profiles.json", "team-links.json", "organization-links.json", "schedules.json"} {
		path := filepath.Join(source, name)
		records, exists, err := loadObject(path)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		filtered := map[string]json.RawMessage{}
		for key, raw := range records {
			if containsSelected(raw, selected) || containsKey(selected, key) {
				filtered[key] = raw
			}
		}
		if err := writeJSON(filepath.Join(destination, name), filtered); err != nil {
			return err
		}
		summary.FilesCopied++
	}

	related, copied, err := filterIntegrations(filepath.Join(source, "integrations.json"), filepath.Join(destination, "integrations.json"), selected)
	if err != nil {
		return err
	}
	if copied {
		summary.FilesCopied++
	}
	for key := range related {
		selected[key] = struct{}{}
	}

	if copied, err := copyProjection(filepath.Join(source, "remote.json"), filepath.Join(destination, "remote.json"), false); err != nil {
		return err
	} else if copied {
		summary.FilesCopied++
	}
	for _, name := range ndjsonFiles {
		copied, err := filterNDJSON(filepath.Join(source, name), filepath.Join(destination, name), selected)
		if err != nil {
			return err
		}
		if copied {
			summary.FilesCopied++
		}
	}
	return nil
}

func filterIntegrations(source, destination string, selected map[string]struct{}) (map[string]struct{}, bool, error) {
	document, exists, err := loadObject(source)
	if err != nil || !exists {
		return nil, false, err
	}
	sections := map[string]map[string]json.RawMessage{}
	for _, name := range []string{"connections", "addresses", "memberships", "conversationCandidates"} {
		section := map[string]json.RawMessage{}
		if raw := document[name]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &section); err != nil {
				return nil, false, fmt.Errorf("decode %s in %s: %w", name, source, err)
			}
		}
		sections[name] = section
	}
	related := map[string]struct{}{}
	addressIDs := map[string]struct{}{}
	connectionIDs := map[string]struct{}{}
	filteredAddresses := map[string]json.RawMessage{}
	for id, raw := range sections["addresses"] {
		var address addressRecord
		if json.Unmarshal(raw, &address) == nil && containsKey(selected, address.AgentID) {
			filteredAddresses[id] = raw
			addressIDs[id] = struct{}{}
			addressIDs[address.ID] = struct{}{}
			connectionIDs[address.ConnectionID] = struct{}{}
			related[id] = struct{}{}
			related[address.ID] = struct{}{}
		}
	}
	sections["addresses"] = filteredAddresses
	filterByID(sections, "connections", connectionIDs, related)

	for _, name := range []string{"memberships", "conversationCandidates"} {
		filtered := map[string]json.RawMessage{}
		for id, raw := range sections[name] {
			var record membershipRecord
			if json.Unmarshal(raw, &record) == nil && containsKey(addressIDs, record.AddressID) {
				filtered[id] = raw
				related[id] = struct{}{}
				related[record.ID] = struct{}{}
			}
		}
		sections[name] = filtered
	}
	for name, section := range sections {
		raw, _ := json.Marshal(section)
		document[name] = raw
	}
	return related, true, writeJSON(destination, document)
}

func filterByID(sections map[string]map[string]json.RawMessage, name string, ids, related map[string]struct{}) {
	filtered := map[string]json.RawMessage{}
	for id, raw := range sections[name] {
		if containsKey(ids, id) || containsSelected(raw, ids) {
			filtered[id] = raw
			related[id] = struct{}{}
		}
	}
	sections[name] = filtered
}

func filterNDJSON(source, destination string, selected map[string]struct{}) (bool, error) {
	input, err := os.Open(source)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer output.Close()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !json.Valid(line) {
			return false, fmt.Errorf("invalid NDJSON record in %s", source)
		}
		if containsSelected(line, selected) {
			if _, err := output.Write(append(append([]byte{}, line...), '\n')); err != nil {
				return false, err
			}
		}
	}
	return true, scanner.Err()
}

func copyProjection(source, destination string, ndjson bool) (bool, error) {
	input, err := os.Open(source)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer output.Close()
	if !ndjson {
		_, err = io.Copy(output, input)
		return true, err
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !json.Valid(line) {
			return false, fmt.Errorf("invalid NDJSON record in %s", source)
		}
		if _, err := output.Write(append(append([]byte{}, line...), '\n')); err != nil {
			return false, err
		}
	}
	return true, scanner.Err()
}

func loadAgents(path string) (map[string]json.RawMessage, error) {
	records, exists, err := loadObject(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("source has no agents.json: %s", path)
	}
	return records, nil
}

func loadObject(path string) (map[string]json.RawMessage, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	records := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, false, fmt.Errorf("decode %s: %w", path, err)
	}
	return records, true, nil
}

func selectAgents(all map[string]json.RawMessage, selectors []string) (map[string]json.RawMessage, error) {
	if len(selectors) == 0 {
		return all, nil
	}
	selected := map[string]json.RawMessage{}
	for _, selector := range selectors {
		found := false
		for key, raw := range all {
			var agent agentRecord
			_ = json.Unmarshal(raw, &agent)
			if selector == key || selector == agent.ID || selector == agent.Name {
				selected[key] = raw
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("Agent not found in snapshot source: %s", selector)
		}
	}
	return selected, nil
}

func summarize(agents map[string]json.RawMessage, filtered bool) Summary {
	summary := Summary{Filtered: filtered}
	for key, raw := range agents {
		var agent agentRecord
		_ = json.Unmarshal(raw, &agent)
		id := agent.ID
		if id == "" {
			id = key
		}
		summary.AgentIDs = append(summary.AgentIDs, id)
		summary.AgentNames = append(summary.AgentNames, agent.Name)
	}
	sort.Strings(summary.AgentIDs)
	sort.Strings(summary.AgentNames)
	return summary
}

func containsSelected(raw []byte, selected map[string]struct{}) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return containsValue(value, selected)
}

func containsValue(value any, selected map[string]struct{}) bool {
	switch typed := value.(type) {
	case string:
		return containsKey(selected, typed)
	case []any:
		for _, item := range typed {
			if containsValue(item, selected) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsValue(item, selected) {
				return true
			}
		}
	}
	return false
}

func containsKey(values map[string]struct{}, key string) bool {
	_, ok := values[key]
	return ok
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
