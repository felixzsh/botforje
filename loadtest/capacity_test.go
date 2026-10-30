package loadtest

// TestCapacity is the opt-in capacity run: it sizes one fleet from the
// environment, drives open-loop traffic, and reports an operating envelope with
// throughput, latency, failures, and sampled resources. It never fails on a
// machine-dependent threshold; ordinary CI keeps only the functional suite.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

type params struct {
	Bots          int
	Scopes        int
	Chats         int
	Rate          float64
	Duration      time.Duration
	Warmup        time.Duration
	Drain         time.Duration
	Payload       int
	MaxPending    int
	Seed          int64
	InjectTimeout time.Duration
	QueueDelayMS  int
	JitterMS      int
	MaxPerMinute  int
	JSON          string
}

// paramsFromEnv reads the workload from BOTFLOATA_LOAD_* variables. An absent
// BOTFLOATA_LOAD_BOTS leaves the run opt-in and skipped.
func paramsFromEnv(t *testing.T) (params, bool) {
	t.Helper()
	raw := os.Getenv("BOTFLOATA_LOAD_BOTS")
	if raw == "" {
		return params{}, false
	}
	p := params{
		Chats:         envInt(t, "BOTFLOATA_LOAD_CHATS", 1),
		Scopes:        envInt(t, "BOTFLOATA_LOAD_SCOPES", 1),
		Rate:          envFloat(t, "BOTFLOATA_LOAD_RATE", 1),
		Duration:      envDuration(t, "BOTFLOATA_LOAD_DURATION", 30*time.Second),
		Warmup:        envDuration(t, "BOTFLOATA_LOAD_WARMUP", 5*time.Second),
		Drain:         envDuration(t, "BOTFLOATA_LOAD_DRAIN", 30*time.Second),
		Payload:       envInt(t, "BOTFLOATA_LOAD_PAYLOAD", 64),
		MaxPending:    envInt(t, "BOTFLOATA_LOAD_MAX_PENDING", 64),
		Seed:          int64(envInt(t, "BOTFLOATA_LOAD_SEED", 1)),
		InjectTimeout: envDuration(t, "BOTFLOATA_LOAD_INJECT_TIMEOUT", 5*time.Second),
		QueueDelayMS:  envInt(t, "BOTFLOATA_LOAD_QUEUE_DELAY_MS", 0),
		JitterMS:      envInt(t, "BOTFLOATA_LOAD_JITTER_MS", 0),
		MaxPerMinute:  envInt(t, "BOTFLOATA_LOAD_MAX_PER_MINUTE", 0),
		JSON:          os.Getenv("BOTFLOATA_LOAD_JSON"),
	}
	p.Bots = envInt(t, "BOTFLOATA_LOAD_BOTS", 0)
	if p.Bots <= 0 || p.Chats <= 0 || p.Scopes <= 0 || p.Rate < 0 {
		t.Fatalf("invalid workload: bots=%d scopes=%d chats=%d rate=%v",
			p.Bots, p.Scopes, p.Chats, p.Rate)
	}
	return p, true
}

func TestCapacity(t *testing.T) {
	p, ok := paramsFromEnv(t)
	if !ok {
		t.Skip("opt-in capacity run: set BOTFLOATA_LOAD_BOTS, " +
			"and optionally BOTFLOATA_LOAD_SCOPES, _CHATS, _RATE, _DURATION, " +
			"_WARMUP, _DRAIN, _PAYLOAD, _MAX_PENDING, _SEED, _JSON")
	}

	l := layout{
		Scopes: p.Scopes, Bots: p.Bots, Chats: p.Chats,
		QueueDelayMS: p.QueueDelayMS, JitterMS: p.JitterMS,
		MaxPerMinute: p.MaxPerMinute,
	}
	h := newHarness(t, l)
	defer h.stop()
	for i := 0; i < l.Bots; i++ {
		h.channel(i).Retain(8)
	}
	tr := newTraffic(l, p)
	for i := 0; i < l.Bots; i++ {
		bot := i
		h.channel(i).OnSend(func(msg botflota.OutgoingMessage) {
			tr.observed(bot, botflota.ContentText(msg.Content))
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if p.Warmup > 0 {
		tr.run(ctx, h, p.Warmup)
		tr.drain(h, p.Drain)
		t.Logf("warm-up: %s failures=%d",
			fmtCounters(tr.totals()), h.errorCount())
		tr.reset()
	}

	failuresBefore := h.errorCount()
	start := runtimeSnapshot()
	begin := time.Now()
	peaks := &peakTracker{}
	sampling := make(chan struct{})
	go func() {
		defer close(sampling)
		trackPeaks(ctx, h, peaks)
	}()
	tr.run(ctx, h, p.Duration)
	wall := time.Since(begin)
	cancel()
	<-sampling
	unfinished := tr.drain(h, p.Drain)
	end := runtimeSnapshot()

	counts := tr.totals()
	counts.Unfinished = unfinished
	rep := report{
		Revision: revision(),
		Machine:  machineInfo(),
		Workload: workload{
			Bots: p.Bots, Scopes: p.Scopes, Chats: p.Chats,
			RatePerBot: p.Rate, PayloadBytes: p.Payload, Seed: p.Seed,
			MaxPendingPerBot: p.MaxPending,
			QueueDelayMS:     p.QueueDelayMS,
			JitterMS:         p.JitterMS,
			MaxPerMinute:     p.MaxPerMinute,
		},
		Measurement: measurement{
			counters:    counts,
			WallSeconds: wall.Seconds(),
			Throughput:  float64(counts.Completed) / wall.Seconds(),
			Failures:    h.errorCount() - failuresBefore,
			Latency:     summarizeLatency(tr.latencies()),
		},
		Resources: resources{
			HeapStartMB: start.heapMB(), HeapEndMB: end.heapMB(),
			HeapPeakMB: peaks.heapMB(),
			RSSStartMB: start.rssMB(), RSSEndMB: end.rssMB(),
			RSSPeakMB:     peaks.rssMB(),
			Goroutines:    end.goroutines,
			GoroutinePeak: peaks.goroutines,
			CPUSeconds:    end.cpu - start.cpu,
			DBSizeMB:      dbSizeMB(h),
		},
		Notes: []string{
			"synthetic channel: no provider protocol, socket, or rate limit is measured",
			"replies use at-least-once delivery after a failed provider-id record",
			"throughput counts correlated replies; failures are reported turns",
		},
	}
	reportLine := rep.String()
	t.Logf("capacity: %s", reportLine)
	if p.JSON != "" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			t.Fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(p.JSON, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
		t.Logf("report written to %s", p.JSON)
	}
}

type counters struct {
	Offered    int `json:"offered"`
	Completed  int `json:"completed"`
	Duplicates int `json:"duplicates"`
	Refused    int `json:"refused"`
	Skipped    int `json:"skipped"`
	Unfinished int `json:"unfinished"`
}

func fmtCounters(c counters) string {
	return fmt.Sprintf("offered=%d completed=%d refused=%d skipped=%d duplicates=%d unfinished=%d",
		c.Offered, c.Completed, c.Refused, c.Skipped, c.Duplicates, c.Unfinished)
}

type report struct {
	Revision    string      `json:"revision"`
	Machine     machine     `json:"machine"`
	Workload    workload    `json:"workload"`
	Measurement measurement `json:"measurement"`
	Resources   resources   `json:"resources"`
	Notes       []string    `json:"notes"`
}

type machine struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	GoVersion  string `json:"go_version"`
	CPUs       int    `json:"cpus"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	MemTotalMB uint64 `json:"mem_total_mb"`
}

type workload struct {
	Bots             int     `json:"bots"`
	Scopes           int     `json:"scopes"`
	Chats            int     `json:"chats"`
	RatePerBot       float64 `json:"rate_per_bot"`
	PayloadBytes     int     `json:"payload_bytes"`
	Seed             int64   `json:"seed"`
	MaxPendingPerBot int     `json:"max_pending_per_bot"`
	QueueDelayMS     int     `json:"queue_delay_ms"`
	JitterMS         int     `json:"jitter_ms"`
	MaxPerMinute     int     `json:"max_messages_per_minute"`
}

type measurement struct {
	counters
	WallSeconds float64 `json:"wall_seconds"`
	Throughput  float64 `json:"completed_per_second"`
	Failures    int     `json:"failures"`
	Latency     latency `json:"latency"`
}

type latency struct {
	Samples int     `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type resources struct {
	HeapStartMB   float64 `json:"heap_start_mb"`
	HeapEndMB     float64 `json:"heap_end_mb"`
	HeapPeakMB    float64 `json:"heap_peak_mb"`
	RSSStartMB    float64 `json:"rss_start_mb"`
	RSSEndMB      float64 `json:"rss_end_mb"`
	RSSPeakMB     float64 `json:"rss_peak_mb"`
	Goroutines    int     `json:"goroutines"`
	GoroutinePeak int     `json:"goroutine_peak"`
	CPUSeconds    float64 `json:"cpu_seconds"`
	DBSizeMB      float64 `json:"db_size_mb"`
}

func (r report) String() string {
	return strings.Join([]string{
		fmt.Sprintf("revision=%s go=%s/%s cpus=%d gomaxprocs=%d",
			r.Revision, r.Machine.GOOS, r.Machine.GOARCH,
			r.Machine.CPUs, r.Machine.GOMAXPROCS),
		fmt.Sprintf("workload: bots=%d scopes=%d chats=%d rate=%.2f/s/bot "+
			"payload=%dB db=%.1fMB",
			r.Workload.Bots, r.Workload.Scopes, r.Workload.Chats,
			r.Workload.RatePerBot, r.Workload.PayloadBytes, r.Resources.DBSizeMB),
		fmt.Sprintf("pacing: queue_delay=%dms jitter=%dms max_per_minute=%d",
			r.Workload.QueueDelayMS, r.Workload.JitterMS,
			r.Workload.MaxPerMinute),
		fmt.Sprintf("measurement: wall=%.1fs completed/s=%.1f failures=%d %s",
			r.Measurement.WallSeconds, r.Measurement.Throughput,
			r.Measurement.Failures, fmtCounters(r.Measurement.counters)),
		fmt.Sprintf("latency: p50=%.0fms p95=%.0fms p99=%.0fms max=%.0fms n=%d",
			r.Measurement.Latency.P50Ms, r.Measurement.Latency.P95Ms,
			r.Measurement.Latency.P99Ms, r.Measurement.Latency.MaxMs,
			r.Measurement.Latency.Samples),
		fmt.Sprintf("resources: heap=%.1f->%.1fMB peak=%.1fMB rss=%.1f->%.1fMB "+
			"peak=%.1fMB goroutines=%d peak=%d cpu=%.1fs",
			r.Resources.HeapStartMB, r.Resources.HeapEndMB, r.Resources.HeapPeakMB,
			r.Resources.RSSStartMB, r.Resources.RSSEndMB, r.Resources.RSSPeakMB,
			r.Resources.Goroutines, r.Resources.GoroutinePeak, r.Resources.CPUSeconds),
	}, "\n\t")
}

func summarizeLatency(samples []time.Duration) latency {
	if len(samples) == 0 {
		return latency{}
	}
	at := func(q float64) float64 {
		i := int(q * float64(len(samples)))
		if i >= len(samples) {
			i = len(samples) - 1
		}
		return float64(samples[i]) / float64(time.Millisecond)
	}
	return latency{
		Samples: len(samples),
		P50Ms:   at(0.50),
		P95Ms:   at(0.95),
		P99Ms:   at(0.99),
		MaxMs:   float64(samples[len(samples)-1]) / float64(time.Millisecond),
	}
}

type snapshot struct {
	heap       uint64
	rss        uint64
	goroutines int
	cpu        float64
}

func (s snapshot) heapMB() float64 { return float64(s.heap) / (1 << 20) }
func (s snapshot) rssMB() float64  { return float64(s.rss) / (1 << 20) }

type peakTracker struct {
	heap, rss  uint64
	goroutines int
}

func (p *peakTracker) heapMB() float64 { return float64(p.heap) / (1 << 20) }
func (p *peakTracker) rssMB() float64  { return float64(p.rss) / (1 << 20) }

// trackPeaks samples live heap, resident memory, and goroutines while the
// measurement runs; the peak is what tells a run from a plateau.
func trackPeaks(ctx context.Context, h *harness, peaks *peakTracker) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s := runtimeSnapshot()
			if s.heap > peaks.heap {
				peaks.heap = s.heap
			}
			if s.rss > peaks.rss {
				peaks.rss = s.rss
			}
			if s.goroutines > peaks.goroutines {
				peaks.goroutines = s.goroutines
			}
		}
	}
}

func runtimeSnapshot() snapshot {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return snapshot{
		heap: stats.HeapAlloc, rss: residentBytes(),
		goroutines: runtime.NumGoroutine(), cpu: processCPUSeconds(),
	}
}

// residentBytes reports the process's resident set where the platform exposes
// it, and zero elsewhere.
func residentBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

func dbSizeMB(h *harness) float64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(h.path + suffix); err == nil {
			total += info.Size()
		}
	}
	return float64(total) / (1 << 20)
}

func machineInfo() machine {
	return machine{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GoVersion: runtime.Version(), CPUs: runtime.NumCPU(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), MemTotalMB: memTotalMB(),
	}
}

func memTotalMB() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "MemTotal:" {
			kb, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return 0
			}
			return kb / 1024
		}
	}
	return 0
}

// revision ties a report to code. A test binary does not always carry VCS
// metadata, so an explicit BOTFLOATA_LOAD_REVISION wins and the stamped
// revision is the fallback.
func revision() string {
	if explicit := os.Getenv("BOTFLOATA_LOAD_REVISION"); explicit != "" {
		return explicit
	}
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "unknown"
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s = %q: %v", name, raw, err)
	}
	return value
}

func envFloat(t *testing.T, name string, fallback float64) float64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", name, raw, err)
	}
	return value
}

func envDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("%s = %q: %v", name, raw, err)
	}
	return value
}
