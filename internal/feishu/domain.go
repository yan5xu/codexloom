package feishu

import (
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

// Domain selects the API and WebSocket control plane used by one app.
// Empty persisted values are legacy Feishu connections and normalize to
// DomainFeishu at the client boundary.
type Domain string

const (
	DomainFeishu Domain = "feishu"
	DomainLark   Domain = "lark"
)

func ParseDomain(value string) (Domain, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(DomainFeishu):
		return DomainFeishu, nil
	case string(DomainLark):
		return DomainLark, nil
	default:
		return "", fmt.Errorf("Lark/Feishu domain must be %q or %q", DomainLark, DomainFeishu)
	}
}

func OpenBaseURL(domain Domain) string {
	if domain == DomainLark {
		return lark.LarkBaseUrl
	}
	return lark.FeishuBaseUrl
}
