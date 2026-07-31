package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func cmdProvider(a args) {
	action := "list"
	if len(a.positional) > 0 {
		action = a.positional[0]
	}
	switch action {
	case "list":
		resp, err := api("GET", "/api/model-providers", nil)
		if err != nil {
			fail(err)
		}
		providers := anySlice(resp["providers"])
		for _, value := range providers {
			provider, _ := value.(map[string]any)
			configured, _ := provider["credentialConfigured"].(bool)
			status := red("credential missing")
			if configured {
				status = green("configured")
			}
			models := stringSlice(provider["models"])
			fmt.Printf("%s  %s  %s", bold(str(provider, "id")), str(provider, "wireApi"), status)
			if len(models) > 0 {
				fmt.Printf("  %s", strings.Join(models, ", "))
			}
			if count := int(num(provider, "boundAgentCount")); count > 0 {
				fmt.Printf("  %s", dim(fmt.Sprintf("%d Agent(s)", count)))
			}
			fmt.Println()
		}
	case "get":
		id := providerArg(a, "provider get <id>")
		resp, err := api("GET", "/api/model-providers/"+url.PathEscape(id), nil)
		if err != nil {
			fail(err)
		}
		printJSON(resp["provider"])
	case "set":
		id := providerArg(a, "provider set <id> [--name NAME --base-url URL --wire-api responses] [--api-key-file PATH|--env-key NAME|--clear-credential]")
		body := map[string]any{
			"name": a.flags["name"], "baseUrl": a.flags["base-url"], "wireApi": a.flags["wire-api"],
			"envKey": a.flags["env-key"], "clearCredential": a.flags["clear-credential"] == "true",
		}
		if path := strings.TrimSpace(a.flags["api-key-file"]); path != "" {
			if err := requireSecureSecretTransport(base); err != nil {
				fail(err)
			}
			apiKey, err := readOwnerOnlySecretFile(path)
			if err != nil {
				fail(fmt.Errorf("read API key file: %w", err))
			}
			body["apiKey"] = apiKey
		}
		resp, err := api("PUT", "/api/model-providers/"+url.PathEscape(id), body)
		delete(body, "apiKey")
		if err != nil {
			fail(err)
		}
		provider, _ := resp["provider"].(map[string]any)
		fmt.Printf("%s %s (%s)\n", green("configured"), bold(str(provider, "name")), str(provider, "id"))
		fmt.Printf("  credential: %s\n", str(provider, "credentialSource"))
	case "disable", "delete":
		id := providerArg(a, "provider disable <id>")
		if _, err := api("DELETE", "/api/model-providers/"+url.PathEscape(id), nil); err != nil {
			fail(err)
		}
		fmt.Printf("%s %s\n", green("disabled"), id)
	case "verify":
		id := providerArg(a, "provider verify <id>")
		resp, err := api("POST", "/api/model-providers/"+url.PathEscape(id)+"/verify", map[string]any{"model": a.flags["model"]})
		if err != nil {
			fail(err)
		}
		printJSON(resp["verification"])
	default:
		usage("provider list|get|set|disable|verify ...")
	}
}

func providerArg(a args, help string) string {
	if len(a.positional) < 2 || strings.TrimSpace(a.positional[1]) == "" {
		usage(help)
	}
	return strings.TrimSpace(a.positional[1])
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(data))
}

func stringSlice(value any) []string {
	items := anySlice(value)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
