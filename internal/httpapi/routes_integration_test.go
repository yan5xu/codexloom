package httpapi

import (
	"testing"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func TestInboxItemMatchesAddressAndActiveFilters(t *testing.T) {
	addresses := stringSet([]string{" addr-a ", "addr-b", ""})
	tests := []struct {
		name       string
		item       hub.InboxItem
		activeOnly bool
		want       bool
	}{
		{name: "selected active address", item: hub.InboxItem{AddressID: "addr-a", State: "queued"}, activeOnly: true, want: true},
		{name: "different address", item: hub.InboxItem{AddressID: "addr-c", State: "queued"}, activeOnly: true, want: false},
		{name: "handled is terminal", item: hub.InboxItem{AddressID: "addr-a", State: "handled"}, activeOnly: true, want: false},
		{name: "cancelled is terminal", item: hub.InboxItem{AddressID: "addr-a", State: "cancelled"}, activeOnly: true, want: false},
		{name: "terminal allowed without active filter", item: hub.InboxItem{AddressID: "addr-b", State: "handled"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inboxItemMatches(tt.item, addresses, tt.activeOnly); got != tt.want {
				t.Fatalf("inboxItemMatches(%+v) = %v, want %v", tt.item, got, tt.want)
			}
		})
	}
}
