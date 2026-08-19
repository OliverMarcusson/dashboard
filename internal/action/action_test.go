package action

import "testing"

func TestForDerivesTheFullVerbSet(t *testing.T) {
	specs := For(KindContainer, "apps-web-1", "Euripus web")
	if len(specs) != 3 {
		t.Fatalf("got %d actions, want start/stop/restart", len(specs))
	}
	for _, s := range specs {
		if s.Confirm == "" {
			t.Errorf("%s has no confirmation sentence — every action needs one", s.ID)
		}
		if s.Target != "apps-web-1" {
			t.Errorf("%s targets %q", s.ID, s.Target)
		}
		if s.Kind != KindContainer {
			t.Errorf("%s has kind %q", s.ID, s.Kind)
		}
	}
	if specs[0].ID != "container.start.apps-web-1" {
		t.Errorf("id = %q", specs[0].ID)
	}
	if specs[0].Label != "Start Euripus web" {
		t.Errorf("label = %q, want the display name", specs[0].Label)
	}
}

func TestForFallsBackToTargetAsName(t *testing.T) {
	specs := For(KindStack, "pvptest", "")
	if specs[2].Label != "Restart pvptest" {
		t.Errorf("label = %q", specs[2].Label)
	}
	if specs[2].Confirm != "Restart the pvptest stack?" {
		t.Errorf("confirm = %q", specs[2].Confirm)
	}
}

func TestParseIDRoundTrips(t *testing.T) {
	for _, kind := range []Kind{KindContainer, KindStack, KindUnit} {
		for _, spec := range For(kind, "some-target", "") {
			gotKind, gotVerb, gotTarget, err := ParseID(spec.ID)
			if err != nil {
				t.Fatalf("%s: %v", spec.ID, err)
			}
			if gotKind != kind || gotVerb != spec.Verb || gotTarget != "some-target" {
				t.Errorf("%s parsed to %s/%s/%s", spec.ID, gotKind, gotVerb, gotTarget)
			}
		}
	}
}

func TestParseIDRejectsJunk(t *testing.T) {
	for _, id := range []string{
		"",
		"container",
		"container.restart",
		"container..name",
		"nonsense.restart.thing",       // unknown kind
		"container.destroy.apps-web-1", // verb outside the vocabulary
		"unit.rm.-rf",
	} {
		if _, _, _, err := ParseID(id); err == nil {
			t.Errorf("ParseID(%q) accepted a malformed id", id)
		}
	}
}

func TestTargetsWithDotsSurviveParsing(t *testing.T) {
	// Unit names carry dots; the id format must not split on them.
	kind, verb, target, err := ParseID("unit.restart.dashboard.service")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if kind != KindUnit || verb != "restart" || target != "dashboard.service" {
		t.Errorf("got %s/%s/%s", kind, verb, target)
	}
}

func TestParseIDRejectsHostileTargets(t *testing.T) {
	for _, id := range []string{
		"unit.stop.docker; rm -rf /",
		"container.restart.name with spaces",
		"stack.stop.$(whoami)",
		"container.stop.a/../../etc/passwd",
		"unit.restart.`id`",
		"container.stop." + string(make([]byte, 200)),
	} {
		if _, _, _, err := ParseID(id); err == nil {
			t.Errorf("ParseID(%q) accepted a hostile target", id)
		}
	}
}

func TestParseIDAcceptsRealNames(t *testing.T) {
	// Names that actually exist on this host must keep working.
	for _, target := range []string{
		"apps-web-1", "mc-18", "velocity-pvptest", "dashboard.service",
		"cloudflare-ddns-dot-1", "user@1000.service", "pwn-09-ret2libc-full",
	} {
		if _, _, got, err := ParseID("container.restart." + target); err != nil {
			t.Errorf("ParseID rejected real name %q: %v", target, err)
		} else if got != target {
			t.Errorf("target = %q, want %q", got, target)
		}
	}
}
