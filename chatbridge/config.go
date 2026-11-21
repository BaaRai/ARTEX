package chatbridge

import (
	"errors"
	"fmt"

	"github.com/Autumn-27/artex/notify"
)

// The masking convention is the one the notification channels use, so the UI
// handles both forms the same way: a credential is echoed as
// notify.MaskedPrefix plus its last characters, sending the masked value back
// unchanged keeps the stored secret, and an empty string clears the field.

// MaskConfig returns a copy of cfg with the transport's credential fields
// masked. An unknown kind yields an empty map rather than the raw config.
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	t, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range t.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			out[k] = notify.MaskedValue(s)
		}
	}
	return out
}

// PrepareConfigUpdate merges an incoming configuration from the API into the
// stored one.
//
// A masked secret keeps the stored value. When a destination field changes
// (for example base_url, which decides where the bot token is sent) every
// secret must be supplied again in clear: a masked value would otherwise send
// the old token to the new address.
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	t, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("chat bridge kind %q is not registered", kind)
	}
	if stored == nil {
		stored = map[string]any{}
	}
	changed := false
	for _, k := range t.DestinationKeys() {
		in, present := incoming[k]
		if !present {
			continue
		}
		if fmt.Sprint(in) != fmt.Sprint(stored[k]) && !(in == "" && stored[k] == nil) {
			changed = true
		}
	}
	if changed {
		for _, k := range t.SecretKeys() {
			s, _ := incoming[k].(string)
			if s == "" || notify.IsMasked(s) {
				return nil, errors.New("the API address changed: enter the bot token again")
			}
		}
	}
	return notify.MergeConfig(stored, incoming), nil
}
