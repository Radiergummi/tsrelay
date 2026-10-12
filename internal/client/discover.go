package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tailscale.com/ipn/ipnstate"
)

// DefaultRelayHost is the relay's default MagicDNS host name.
const DefaultRelayHost = "tsrelay"

// RelayURL returns the relay base URL: explicit if given (https:// is
// assumed without a scheme), else https://tsrelay.<MagicDNS suffix> of the
// tailnet this machine is on.
func RelayURL(ctx context.Context, explicit string, status func(context.Context) (*ipnstate.Status, error)) (string, error) {
	if explicit != "" {
		if !strings.Contains(explicit, "://") {
			explicit = "https://" + explicit
		}
		return strings.TrimSuffix(explicit, "/"), nil
	}
	st, err := status(ctx)
	if err != nil {
		return "", fmt.Errorf("asking the local Tailscale for the tailnet name (is Tailscale running?): %w", err)
	}
	if st.CurrentTailnet == nil || st.CurrentTailnet.MagicDNSSuffix == "" {
		return "", errors.New("could not determine the tailnet's MagicDNS suffix; pass --relay or set TSRELAY_RELAY")
	}
	return "https://" + DefaultRelayHost + "." + st.CurrentTailnet.MagicDNSSuffix, nil
}
