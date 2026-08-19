package probe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dockerHub is the registry an unqualified image name refers to.
const dockerHub = "registry-1.docker.io"

// credentials reads the same file `docker login` writes.
//
// The gh CLI token is deliberately not used here: its default scopes omit
// read:packages, so it cannot read GHCR manifests. Docker's own store already
// holds working credentials for every registry these images are pushed to.
type credentials struct {
	once  sync.Once
	auths map[string]string // registry host -> base64(user:secret)
}

var creds credentials

func (c *credentials) load() {
	c.once.Do(func() {
		c.auths = map[string]string{}

		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		blob, err := os.ReadFile(filepath.Join(home, ".docker", "config.json"))
		if err != nil {
			return
		}
		var cfg struct {
			Auths map[string]struct {
				Auth string `json:"auth"`
			} `json:"auths"`
		}
		if json.Unmarshal(blob, &cfg) != nil {
			return
		}
		for host, entry := range cfg.Auths {
			if entry.Auth == "" {
				continue
			}
			// Entries may be full URLs; the host is what matters.
			host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
			host = strings.TrimSuffix(host, "/")
			if host == "index.docker.io" || host == "docker.io" {
				host = dockerHub
			}
			c.auths[host] = entry.Auth
		}
	})
}

func (c *credentials) for_(registry string) (string, bool) {
	c.load()
	auth, ok := c.auths[registry]
	return auth, ok
}

// ref is a parsed image reference.
type ref struct {
	Registry string
	Repo     string
	Tag      string
	Digest   string

	// ImplicitLibrary is set when a bare name like "postgres" was expanded to
	// "library/postgres". A real Hub image resolves anonymously; a locally
	// built image with the same shape does not exist there at all, and that
	// difference is how the two are told apart.
	ImplicitLibrary bool
}

// parseRef splits an image reference into registry, repository, and tag.
func parseRef(image string) (ref, error) {
	r := ref{Registry: dockerHub, Tag: "latest"}

	if at := strings.LastIndex(image, "@"); at >= 0 {
		r.Digest = image[at+1:]
		image = image[:at]
	}

	parts := strings.SplitN(image, "/", 2)
	// A first segment containing a dot, a colon, or "localhost" is a registry.
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		r.Registry = parts[0]
		image = parts[1]
	}
	// docker.io is the canonical name but not the API host; the registry API
	// lives on registry-1.docker.io and the alias does not serve manifests.
	if r.Registry == "docker.io" || r.Registry == "index.docker.io" {
		r.Registry = dockerHub
	}

	if colon := strings.LastIndex(image, ":"); colon >= 0 && !strings.Contains(image[colon:], "/") {
		r.Tag = image[colon+1:]
		image = image[:colon]
	}
	if image == "" {
		return r, fmt.Errorf("could not parse image reference")
	}

	// Docker Hub official images live under library/.
	if r.Registry == dockerHub && !strings.Contains(image, "/") {
		image = "library/" + image
		r.ImplicitLibrary = true
	}
	r.Repo = image
	return r, nil
}

// Local returns true for images that exist only on this host.
func (r ref) Local() bool {
	return r.Registry == "localhost" || strings.HasPrefix(r.Registry, "localhost:")
}

var registryClient = &http.Client{Timeout: 20 * time.Second}

// remoteDigest resolves the digest a tag currently points at.
func remoteDigest(ctx context.Context, r ref) (string, error) {
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", r.Registry, r.Repo, r.Tag)

	token, err := authToken(ctx, r)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", err
	}
	// Accept every manifest shape, or a multi-arch image answers 404.
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", token)
	}

	res, err := registryClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", statusError(r, res.StatusCode)
	}
	digest := res.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("registry did not report a digest")
	}
	return digest, nil
}

// authToken performs the registry token dance: an unauthenticated probe returns
// a challenge naming where to get a token, which is then fetched using the
// stored credentials if there are any.
func authToken(ctx context.Context, r ref) (string, error) {
	probeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+r.Registry+"/v2/", nil)
	if err != nil {
		return "", err
	}
	probeRes, err := registryClient.Do(probeReq)
	if err != nil {
		return "", err
	}
	defer probeRes.Body.Close()

	if probeRes.StatusCode == http.StatusOK {
		return "", nil // registry allows anonymous access
	}

	challenge := probeRes.Header.Get("Www-Authenticate")
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		// Basic auth registries take the stored credential directly.
		if auth, ok := creds.for_(r.Registry); ok {
			return "Basic " + auth, nil
		}
		return "", fmt.Errorf("no credentials for %s", r.Registry)
	}

	params := map[string]string{}
	for _, part := range strings.Split(challenge[len("bearer "):], ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			params[strings.ToLower(kv[0])] = strings.Trim(kv[1], `"`)
		}
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("registry gave no token endpoint")
	}

	tokenURL := fmt.Sprintf("%s?service=%s&scope=repository:%s:pull", realm, params["service"], r.Repo)
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	if auth, ok := creds.for_(r.Registry); ok {
		// The stored value is already base64(user:secret).
		if _, err := base64.StdEncoding.DecodeString(auth); err == nil {
			tokenReq.Header.Set("Authorization", "Basic "+auth)
		}
	}

	tokenRes, err := registryClient.Do(tokenReq)
	if err != nil {
		return "", err
	}
	defer tokenRes.Body.Close()

	if tokenRes.StatusCode != http.StatusOK {
		return "", statusError(r, tokenRes.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenRes.Body).Decode(&body); err != nil {
		return "", err
	}
	token := body.Token
	if token == "" {
		token = body.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("registry returned an empty token")
	}
	return "Bearer " + token, nil
}

// statusError turns a registry rejection into something a person can act on.
//
// A bare image name the registry will not serve was never pushed anywhere: it
// was built on this host and given a name that merely looks like a Hub image.
func statusError(r ref, status int) error {
	if r.ImplicitLibrary && (status == http.StatusUnauthorized || status == http.StatusNotFound) {
		return fmt.Errorf("built locally, never pushed to a registry")
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("no credentials for %s", r.Registry)
	case http.StatusNotFound:
		return fmt.Errorf("%s:%s is not in %s", r.Repo, r.Tag, r.Registry)
	}
	return fmt.Errorf("%s returned %d", r.Registry, status)
}
