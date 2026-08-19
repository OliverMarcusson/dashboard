package probe

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct{ image, registry, repo, tag string }{
		// Bare Docker Hub names get the implicit library/ namespace.
		{"postgres:17", dockerHub, "library/postgres", "17"},
		{"redis:7-alpine", dockerHub, "library/redis", "7-alpine"},
		{"alpine", dockerHub, "library/alpine", "latest"},
		// A user namespace stays as written.
		{"itzg/minecraft-server:java8", dockerHub, "itzg/minecraft-server", "java8"},
		{"adguard/adguardhome:v0.107.71", dockerHub, "adguard/adguardhome", "v0.107.71"},
		// docker.io is an alias for the API host, which is different.
		{"docker.io/library/postgres:17", dockerHub, "library/postgres", "17"},
		{"docker.io/qmcgaw/gluetun:v3.41.1", dockerHub, "qmcgaw/gluetun", "v3.41.1"},
		{"index.docker.io/library/redis:7", dockerHub, "library/redis", "7"},
		// A real third-party registry.
		{"ghcr.io/element-hq/synapse:latest", "ghcr.io", "element-hq/synapse", "latest"},
		{"ghcr.io/olivermarcusson/euripus-web:selfhosted-latest", "ghcr.io", "olivermarcusson/euripus-web", "selfhosted-latest"},
		// Local builds.
		{"localhost/caddy-layer4:2.11.4", "localhost", "caddy-layer4", "2.11.4"},
	}
	for _, tc := range cases {
		got, err := parseRef(tc.image)
		if err != nil {
			t.Errorf("parseRef(%q): %v", tc.image, err)
			continue
		}
		if got.Registry != tc.registry || got.Repo != tc.repo || got.Tag != tc.tag {
			t.Errorf("parseRef(%q) = %s / %s / %s, want %s / %s / %s",
				tc.image, got.Registry, got.Repo, got.Tag, tc.registry, tc.repo, tc.tag)
		}
	}
}

func TestParseRefDigest(t *testing.T) {
	r, err := parseRef("ghcr.io/olivermarcusson/euripus-web@sha256:7116e2c8")
	if err != nil {
		t.Fatal(err)
	}
	if r.Digest != "sha256:7116e2c8" || r.Repo != "olivermarcusson/euripus-web" {
		t.Errorf("got digest=%q repo=%q", r.Digest, r.Repo)
	}
}

func TestLocalDetection(t *testing.T) {
	for _, image := range []string{"localhost/marcusson-pages:latest", "localhost:5000/thing:v1"} {
		r, _ := parseRef(image)
		if !r.Local() {
			t.Errorf("%q should be recognised as a local image", image)
		}
	}
	r, _ := parseRef("ghcr.io/element-hq/synapse:latest")
	if r.Local() {
		t.Error("ghcr.io should not be treated as local")
	}
}

func TestImplicitLibraryMarking(t *testing.T) {
	// Bare names are the shape a local build shares with a Hub official image.
	for _, image := range []string{"postgres:17", "pwn-challenges:latest", "wol-wol-http"} {
		r, _ := parseRef(image)
		if !r.ImplicitLibrary {
			t.Errorf("%q should be marked implicit-library", image)
		}
	}
	for _, image := range []string{"itzg/minecraft-server:java8", "ghcr.io/element-hq/synapse:latest"} {
		r, _ := parseRef(image)
		if r.ImplicitLibrary {
			t.Errorf("%q should not be marked implicit-library", image)
		}
	}
}

func TestStatusErrorExplainsLocalBuilds(t *testing.T) {
	local, _ := parseRef("pwn-challenges:latest")
	if got := statusError(local, 401).Error(); got != "built locally, never pushed to a registry" {
		t.Errorf("bare name 401 => %q", got)
	}
	// A namespaced private image is a credentials problem, not a local build.
	private, _ := parseRef("ghcr.io/olivermarcusson/euripus-web:selfhosted-latest")
	if got := statusError(private, 401).Error(); got != "no credentials for ghcr.io" {
		t.Errorf("private image 401 => %q", got)
	}
}
