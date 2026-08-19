// Package probe collects the things that need asking rather than watching:
// disk reclaim, drive health, certificate expiry, image drift.
package probe

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

const TopicStorage = "probe.storage"

// DockerSpace is what Docker is holding and what could be released.
type DockerSpace struct {
	Kind        string `json:"kind"`
	Total       int64  `json:"total"`
	Active      int64  `json:"active"`
	Size        int64  `json:"size"`
	Reclaimable int64  `json:"reclaimable"`
}

// Drive is one physical disk's SMART summary.
type Drive struct {
	Device      string `json:"device"`
	Model       string `json:"model,omitempty"`
	Serial      string `json:"serial,omitempty"`
	Healthy     bool   `json:"healthy"`
	Temperature int    `json:"temperature,omitempty"`
	PowerOnHrs  int    `json:"power_on_hours,omitempty"`
	PercentUsed int    `json:"percent_used,omitempty"`
	Capacity    int64  `json:"capacity,omitempty"`
	Error       string `json:"error,omitempty"`
}

type Storage struct {
	Docker      []DockerSpace `json:"docker"`
	Reclaimable int64         `json:"reclaimable_total"`
	Drives      []Drive       `json:"drives"`
	At          time.Time     `json:"at"`
	Error       string        `json:"error,omitempty"`
}

type StorageProbe struct {
	hub      *hub.Hub
	docker   *client.Client
	interval time.Duration
}

func NewStorage(h *hub.Hub, docker *client.Client, interval time.Duration) *StorageProbe {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &StorageProbe{hub: h, docker: docker, interval: interval}
}

func (p *StorageProbe) Name() string { return "probe.storage" }

func (p *StorageProbe) Run(ctx context.Context) error {
	return loop(ctx, p.interval, func() {
		snap := p.Snapshot(ctx)
		p.hub.Publish(TopicStorage, snap)
	})
}

func (p *StorageProbe) Snapshot(ctx context.Context) Storage {
	s := Storage{At: time.Now().UTC(), Docker: []DockerSpace{}, Drives: []Drive{}}

	if p.docker != nil {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		du, err := p.docker.DiskUsage(ctx, client.DiskUsageOptions{
			Containers: true, Images: true, Volumes: true, BuildCache: true,
		})
		if err != nil {
			s.Error = err.Error()
		} else {
			s.Docker = []DockerSpace{
				{Kind: "images", Total: du.Images.TotalCount, Active: du.Images.ActiveCount,
					Size: du.Images.TotalSize, Reclaimable: du.Images.Reclaimable},
				{Kind: "containers", Total: du.Containers.TotalCount, Active: du.Containers.ActiveCount,
					Size: du.Containers.TotalSize, Reclaimable: du.Containers.Reclaimable},
				{Kind: "volumes", Total: du.Volumes.TotalCount, Active: du.Volumes.ActiveCount,
					Size: du.Volumes.TotalSize, Reclaimable: du.Volumes.Reclaimable},
				{Kind: "build cache", Total: du.BuildCache.TotalCount, Active: du.BuildCache.ActiveCount,
					Size: du.BuildCache.TotalSize, Reclaimable: du.BuildCache.Reclaimable},
			}
			for _, d := range s.Docker {
				s.Reclaimable += d.Reclaimable
			}
		}
	}

	s.Drives = smart(ctx)
	return s
}

// smartScan is the shape of `smartctl --scan --json`.
type smartScan struct {
	Devices []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"devices"`
}

// smartInfo covers the subset of `smartctl -HAi --json` we display. NVMe and
// ATA report health in different places, so both are read.
type smartInfo struct {
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	SmartStatus  struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime struct {
		Hours int `json:"hours"`
	} `json:"power_on_time"`
	NVMeHealth struct {
		PercentageUsed int `json:"percentage_used"`
	} `json:"nvme_smart_health_information_log"`
	UserCapacity struct {
		Bytes int64 `json:"bytes"`
	} `json:"user_capacity"`
	NVMeCapacity int64 `json:"nvme_total_capacity"`
}

// smart reads drive health via smartctl. Arguments are passed directly; no
// shell is involved.
func smart(ctx context.Context) []Drive {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "smartctl", "--scan", "--json").Output()
	if err != nil {
		return []Drive{}
	}
	var scan smartScan
	if err := json.Unmarshal(out, &scan); err != nil {
		return []Drive{}
	}

	drives := make([]Drive, 0, len(scan.Devices))
	for _, dev := range scan.Devices {
		d := Drive{Device: dev.Name}

		args := []string{"-H", "-A", "-i", "--json"}
		if dev.Type != "" {
			args = append(args, "-d", dev.Type)
		}
		args = append(args, dev.Name)

		// smartctl uses its exit status for bitflags, not just failure, so the
		// output is parsed regardless of exit code.
		raw, _ := exec.CommandContext(ctx, "smartctl", args...).Output()
		var info smartInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			d.Error = "could not read SMART data"
			drives = append(drives, d)
			continue
		}

		d.Model = strings.TrimSpace(info.ModelName)
		d.Serial = strings.TrimSpace(info.SerialNumber)
		d.Healthy = info.SmartStatus.Passed
		d.Temperature = info.Temperature.Current
		d.PowerOnHrs = info.PowerOnTime.Hours
		d.PercentUsed = info.NVMeHealth.PercentageUsed
		d.Capacity = info.UserCapacity.Bytes
		if d.Capacity == 0 {
			d.Capacity = info.NVMeCapacity
		}
		drives = append(drives, d)
	}
	return drives
}

// loop runs fn immediately and then on an interval until ctx ends.
func loop(ctx context.Context, every time.Duration, fn func()) error {
	fn()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			fn()
		}
	}
}
