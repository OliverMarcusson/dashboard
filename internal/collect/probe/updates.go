package probe

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

const TopicUpdates = "probe.updates"

// ImageStatus compares what a container is running against what its tag now
// points at in the registry.
type ImageStatus struct {
	Container string `json:"container"`
	Stack     string `json:"stack"`
	Image     string `json:"image"`
	Local     string `json:"local_digest,omitempty"`
	Remote    string `json:"remote_digest,omitempty"`
	Behind    bool   `json:"behind"`
	Checked   bool   `json:"checked"`
	Reason    string `json:"reason,omitempty"`
}

type Updates struct {
	Images         []ImageStatus `json:"images"`
	Behind         int           `json:"behind"`
	Packages       int           `json:"packages"`
	PackagesKnown  bool          `json:"packages_known"`
	RebootRequired bool          `json:"reboot_required"`
	At             time.Time     `json:"at"`
}

type UpdatesProbe struct {
	hub      *hub.Hub
	docker   *client.Client
	interval time.Duration
}

func NewUpdates(h *hub.Hub, docker *client.Client, interval time.Duration) *UpdatesProbe {
	if interval <= 0 {
		interval = time.Hour
	}
	return &UpdatesProbe{hub: h, docker: docker, interval: interval}
}

func (p *UpdatesProbe) Name() string { return "probe.updates" }

func (p *UpdatesProbe) Run(ctx context.Context) error {
	return loop(ctx, p.interval, func() {
		p.hub.Publish(TopicUpdates, p.Snapshot(ctx))
	})
}

func (p *UpdatesProbe) Snapshot(ctx context.Context) Updates {
	u := Updates{At: time.Now().UTC(), Images: []ImageStatus{}}

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	u.Images = p.images(ctx)
	for _, img := range u.Images {
		if img.Behind {
			u.Behind++
		}
	}
	u.Packages, u.PackagesKnown = packageUpdates(ctx)
	u.RebootRequired = rebootRequired(ctx)
	return u
}

func (p *UpdatesProbe) images(ctx context.Context) []ImageStatus {
	if p.docker == nil {
		return []ImageStatus{}
	}
	list, err := p.docker.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return []ImageStatus{}
	}

	// One check per distinct image, not per container: several containers
	// commonly share an image, and each check is a network round trip.
	type target struct {
		image string
		local string
	}
	byImage := map[string][]ImageStatus{}
	locals := map[string]string{}

	for _, c := range list.Items {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		st := ImageStatus{
			Container: name,
			Stack:     c.Labels["com.docker.compose.project"],
			Image:     c.Image,
		}
		if _, seen := locals[c.Image]; !seen {
			locals[c.Image] = ""
			if info, err := p.docker.ImageInspect(ctx, c.Image); err == nil {
				if len(info.RepoDigests) > 0 {
					if at := strings.LastIndex(info.RepoDigests[0], "@"); at >= 0 {
						locals[c.Image] = info.RepoDigests[0][at+1:]
					}
				}
			}
		}
		st.Local = locals[c.Image]
		byImage[c.Image] = append(byImage[c.Image], st)
	}

	remotes := make(map[string]target, len(byImage))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for image := range byImage {
		wg.Add(1)
		go func(image string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r, err := parseRef(image)
			if err != nil {
				mu.Lock()
				remotes[image] = target{local: "could not parse the image reference"}
				mu.Unlock()
				return
			}
			if r.Local() {
				mu.Lock()
				remotes[image] = target{local: "built locally, no registry to compare against"}
				mu.Unlock()
				return
			}
			digest, err := remoteDigest(ctx, r)
			if err != nil {
				mu.Lock()
				remotes[image] = target{local: err.Error()}
				mu.Unlock()
				return
			}
			mu.Lock()
			remotes[image] = target{image: digest}
			mu.Unlock()
		}(image)
	}
	wg.Wait()

	out := []ImageStatus{}
	for image, statuses := range byImage {
		res := remotes[image]
		for _, st := range statuses {
			if res.image != "" {
				st.Remote = res.image
				st.Checked = true
				st.Behind = st.Local != "" && st.Local != res.image
			} else {
				st.Reason = res.local
			}
			out = append(out, st)
		}
	}

	// Anything behind first, then anything that could not be checked.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Behind != b.Behind {
			return a.Behind
		}
		if a.Checked != b.Checked {
			return !a.Checked
		}
		return a.Container < b.Container
	})
	return out
}

// packageUpdates counts pending dnf updates. dnf exits 100 when there are some,
// 0 when there are none, which is why the exit code is inspected rather than
// treated as failure.
func packageUpdates(ctx context.Context) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dnf", "--quiet", "--cacheonly", "check-update")
	out, err := cmd.Output()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		return 0, false
	}
	if code != 0 && code != 100 {
		return 0, false
	}

	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Package lines are "name.arch version repo"; headers and blanks are not.
		if line == "" || strings.HasPrefix(line, "Obsoleting") || strings.HasPrefix(line, "Last metadata") {
			continue
		}
		if len(strings.Fields(line)) >= 3 {
			count++
		}
	}
	return count, true
}

// rebootRequired reports whether running processes still use replaced files.
func rebootRequired(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	err := exec.CommandContext(ctx, "dnf", "needs-restarting", "--reboothint").Run()
	exit, ok := err.(*exec.ExitError)
	return ok && exit.ExitCode() == 1
}
