// Package host collects vitals for the machine itself.
package host

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/olivermarcusson/dashboard/internal/hub"
	"github.com/olivermarcusson/dashboard/internal/store"
)

const Topic = "host.metrics"

// Metrics is the snapshot published each tick.
type Metrics struct {
	CPUPercent  float64      `json:"cpu_percent"`
	MemUsed     uint64       `json:"mem_used"`
	MemTotal    uint64       `json:"mem_total"`
	MemPercent  float64      `json:"mem_percent"`
	SwapUsed    uint64       `json:"swap_used"`
	SwapTotal   uint64       `json:"swap_total"`
	Load1       float64      `json:"load1"`
	Load5       float64      `json:"load5"`
	Load15      float64      `json:"load15"`
	UptimeSecs  uint64       `json:"uptime_secs"`
	BootTime    uint64       `json:"boot_time"`
	Hostname    string       `json:"hostname"`
	Platform    string       `json:"platform"`
	KernelVer   string       `json:"kernel_version"`
	NetRxPerSec float64      `json:"net_rx_per_sec"`
	NetTxPerSec float64      `json:"net_tx_per_sec"`
	Filesystems []Filesystem `json:"filesystems"`
	Temps       []Temp       `json:"temps,omitempty"`
	At          time.Time    `json:"at"`
}

type Filesystem struct {
	Mount   string  `json:"mount"`
	Device  string  `json:"device"`
	FSType  string  `json:"fstype"`
	Total   uint64  `json:"total"`
	Used    uint64  `json:"used"`
	Free    uint64  `json:"free"`
	Percent float64 `json:"percent"`
}

type Temp struct {
	Sensor string  `json:"sensor"`
	Value  float64 `json:"value"`
}

type Collector struct {
	hub      *hub.Hub
	db       *store.DB
	interval time.Duration

	lastNetRx, lastNetTx uint64
	lastNetAt            time.Time
}

func New(h *hub.Hub, db *store.DB, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Collector{hub: h, db: db, interval: interval}
}

func (c *Collector) Name() string { return "host" }

func (c *Collector) Run(ctx context.Context) error {
	t := time.NewTicker(c.interval)
	defer t.Stop()

	// Persist at a coarser cadence than we publish: the UI wants smooth live
	// updates, the database wants a manageable row count.
	persist := time.NewTicker(10 * time.Second)
	defer persist.Stop()

	var latest *Metrics
	for {
		m, err := c.sample(ctx)
		if err != nil {
			slog.Warn("host sample failed", "err", err)
		} else {
			latest = m
			c.hub.Publish(Topic, m)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-persist.C:
			if latest != nil {
				if err := c.persist(ctx, latest); err != nil {
					slog.Warn("host persist failed", "err", err)
				}
			}
		case <-t.C:
		}
	}
}

func (c *Collector) sample(ctx context.Context) (*Metrics, error) {
	m := &Metrics{At: time.Now().UTC()}

	if pcts, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pcts) > 0 {
		m.CPUPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemUsed, m.MemTotal, m.MemPercent = vm.Used, vm.Total, vm.UsedPercent
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		m.SwapUsed, m.SwapTotal = sw.Used, sw.Total
	}
	if avg, err := load.AvgWithContext(ctx); err == nil {
		m.Load1, m.Load5, m.Load15 = avg.Load1, avg.Load5, avg.Load15
	}
	if info, err := host.InfoWithContext(ctx); err == nil {
		m.UptimeSecs, m.BootTime = info.Uptime, info.BootTime
		m.Hostname, m.Platform, m.KernelVer = info.Hostname, info.Platform, info.KernelVersion
	}

	m.Filesystems = c.filesystems(ctx)
	m.Temps = c.temps(ctx)
	c.network(ctx, m)
	return m, nil
}

// filesystems reports real mounts only. Docker's per-container overlay mounts
// all report the same underlying device and would otherwise dominate the list.
func (c *Collector) filesystems(ctx context.Context) []Filesystem {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	out := make([]Filesystem, 0, 4)
	for _, p := range parts {
		if strings.HasPrefix(p.Mountpoint, "/var/lib/docker/") ||
			strings.HasPrefix(p.Mountpoint, "/run/") ||
			strings.HasPrefix(p.Mountpoint, "/sys/") ||
			strings.HasPrefix(p.Mountpoint, "/proc/") {
			continue
		}
		if seen[p.Device] {
			continue
		}
		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}
		seen[p.Device] = true
		out = append(out, Filesystem{
			Mount: p.Mountpoint, Device: p.Device, FSType: p.Fstype,
			Total: usage.Total, Used: usage.Used, Free: usage.Free, Percent: usage.UsedPercent,
		})
	}
	return out
}

func (c *Collector) temps(ctx context.Context) []Temp {
	readings, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		return nil
	}
	out := make([]Temp, 0, len(readings))
	for _, r := range readings {
		if r.Temperature <= 0 {
			continue
		}
		out = append(out, Temp{Sensor: r.SensorKey, Value: r.Temperature})
	}
	return out
}

// network converts cumulative interface counters into a per-second rate.
func (c *Collector) network(ctx context.Context, m *Metrics) {
	counters, err := net.IOCountersWithContext(ctx, false)
	if err != nil || len(counters) == 0 {
		return
	}
	rx, tx := counters[0].BytesRecv, counters[0].BytesSent
	now := time.Now()

	if !c.lastNetAt.IsZero() {
		if elapsed := now.Sub(c.lastNetAt).Seconds(); elapsed > 0 {
			// Counters reset when an interface does; a negative delta is not a rate.
			if rx >= c.lastNetRx {
				m.NetRxPerSec = float64(rx-c.lastNetRx) / elapsed
			}
			if tx >= c.lastNetTx {
				m.NetTxPerSec = float64(tx-c.lastNetTx) / elapsed
			}
		}
	}
	c.lastNetRx, c.lastNetTx, c.lastNetAt = rx, tx, now
}

func (c *Collector) persist(ctx context.Context, m *Metrics) error {
	ts := time.Now().UTC()
	samples := []store.Sample{
		{Kind: "host", Metric: "cpu", Value: m.CPUPercent, TS: ts},
		{Kind: "host", Metric: "mem", Value: m.MemPercent, TS: ts},
		{Kind: "host", Metric: "mem_used", Value: float64(m.MemUsed), TS: ts},
		{Kind: "host", Metric: "load1", Value: m.Load1, TS: ts},
		{Kind: "host", Metric: "net_rx", Value: m.NetRxPerSec, TS: ts},
		{Kind: "host", Metric: "net_tx", Value: m.NetTxPerSec, TS: ts},
	}
	for _, fs := range m.Filesystems {
		samples = append(samples,
			store.Sample{Kind: "disk", Subject: fs.Mount, Metric: "used", Value: float64(fs.Used), TS: ts},
			store.Sample{Kind: "disk", Subject: fs.Mount, Metric: "percent", Value: fs.Percent, TS: ts},
		)
	}
	return c.db.WriteSamples(ctx, samples)
}
