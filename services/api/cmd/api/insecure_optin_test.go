package main

import "testing"

// The fail-closed security defaults hinge on insecureOptIn: only an
// explicit, affirmative value turns a downgrade on. Anything else — unset,
// empty, "0", "false", or a typo — must keep the server fail-closed.
func TestInsecureOptIn(t *testing.T) {
	on := []string{"1", "true", "TRUE", "yes", "on", " on ", "On"}
	off := []string{"", "0", "false", "no", "off", "2", "enable", "y", "t"}

	for _, v := range on {
		t.Setenv("ALLOW_OPEN_API", v)
		if !insecureOptIn("ALLOW_OPEN_API") {
			t.Errorf("value %q should enable the opt-in", v)
		}
	}
	for _, v := range off {
		t.Setenv("ALLOW_OPEN_API", v)
		if insecureOptIn("ALLOW_OPEN_API") {
			t.Errorf("value %q must NOT enable the opt-in (fail-closed)", v)
		}
	}
	// Unset entirely is fail-closed.
	if insecureOptIn("DEPLOY_DEFINITELY_UNSET_FLAG_XYZ") {
		t.Error("unset flag must be fail-closed")
	}
}
