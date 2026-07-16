package hub

import "time"

const connectorClaimLease = 2 * time.Minute

func leaseExpired(expiresAt string, currentTime time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	return err != nil || !expires.After(currentTime)
}
