package speedsmoother

import (
	"regexp"
	"strings"
	"time"
)

type Config struct {
	MinInterval    time.Duration
	WindowDuration time.Duration
	EMAAlpha       float64
	SpikeRatio     float64
	WarmupDuration time.Duration
	PhaseCooldown  time.Duration
	RiseCapRatio   float64
	IdleGrace      time.Duration
	IdleDecay      time.Duration
}

func DefaultConfig() Config {
	return Config{
		MinInterval:    250 * time.Millisecond,
		WindowDuration: 2500 * time.Millisecond,
		EMAAlpha:       0.44,
		SpikeRatio:     5,
		WarmupDuration: 600 * time.Millisecond,
		PhaseCooldown:  300 * time.Millisecond,
		RiseCapRatio:   2.5,
		IdleGrace:      500 * time.Millisecond,
		IdleDecay:      2200 * time.Millisecond,
	}
}

type Sample struct {
	Instant float64
	Display float64
	Average float64
	Peak    float64
}

type Tracker struct {
	cfg Config

	startedAt time.Time
	phaseKey  string
	cooldown  time.Time

	anchorAt    time.Time
	anchorBytes int64
	activeAt    time.Time

	totalBytes int64
	instant    float64
	display    float64
	peak       float64

	window []windowPoint
}

type windowPoint struct {
	at    time.Time
	bytes int64
}

func New(cfg Config) *Tracker {
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = DefaultConfig().MinInterval
	}
	if cfg.WindowDuration <= 0 {
		cfg.WindowDuration = DefaultConfig().WindowDuration
	}
	if cfg.EMAAlpha <= 0 || cfg.EMAAlpha > 1 {
		cfg.EMAAlpha = DefaultConfig().EMAAlpha
	}
	if cfg.SpikeRatio <= 0 {
		cfg.SpikeRatio = DefaultConfig().SpikeRatio
	}
	if cfg.WarmupDuration <= 0 {
		cfg.WarmupDuration = DefaultConfig().WarmupDuration
	}
	if cfg.PhaseCooldown <= 0 {
		cfg.PhaseCooldown = DefaultConfig().PhaseCooldown
	}
	if cfg.RiseCapRatio <= 0 {
		cfg.RiseCapRatio = DefaultConfig().RiseCapRatio
	}
	if cfg.IdleGrace <= 0 {
		cfg.IdleGrace = DefaultConfig().IdleGrace
	}
	if cfg.IdleDecay <= 0 {
		cfg.IdleDecay = DefaultConfig().IdleDecay
	}
	return &Tracker{cfg: cfg}
}

func NewDefault() *Tracker {
	return New(DefaultConfig())
}

func (t *Tracker) Reset() {
	*t = *New(t.cfg)
}

func (t *Tracker) Sample(totalBytes int64, now time.Time, phaseKey string) Sample {
	if t == nil {
		return Sample{}
	}
	if t.cfg.MinInterval <= 0 {
		t.cfg = DefaultConfig()
	}
	phaseKey = strings.TrimSpace(phaseKey)
	if phaseKey == "" {
		phaseKey = "default"
	}

	if totalBytes < t.totalBytes {
		t.anchorBytes = totalBytes
		t.anchorAt = now
		t.window = nil
	}
	t.totalBytes = totalBytes

	if t.startedAt.IsZero() {
		t.startedAt = now
		t.phaseKey = phaseKey
		t.anchorAt = now
		t.anchorBytes = totalBytes
		if totalBytes > 0 {
			t.activeAt = now
		}
		t.appendWindow(now, totalBytes)
		return Sample{}
	}

	if phaseKey != t.phaseKey {
		oldPhase := t.phaseKey
		t.phaseKey = phaseKey
		t.anchorAt = now
		t.anchorBytes = totalBytes
		t.window = nil
		t.appendWindow(now, totalBytes)
		if !isActiveTransferPhase(phaseKey) {
			return t.decayDisplay(now, true)
		}
		if isSlicePhase(oldPhase) && isSlicePhase(phaseKey) && oldPhase != phaseKey {
			t.cooldown = now.Add(t.cfg.PhaseCooldown)
		}
		return Sample{Display: t.display, Average: t.average(now), Peak: t.peak}
	}

	if t.anchorAt.IsZero() {
		t.anchorAt = now
		t.anchorBytes = totalBytes
		return Sample{Display: t.display, Average: t.average(now), Peak: t.peak}
	}

	elapsed := now.Sub(t.anchorAt)
	if elapsed < t.cfg.MinInterval {
		return Sample{Instant: t.instant, Display: t.display, Average: t.average(now), Peak: t.peak}
	}
	if totalBytes <= t.anchorBytes {
		return t.decayDisplay(now, false)
	}

	dt := elapsed.Seconds()
	if dt <= 0 {
		return Sample{Instant: t.instant, Display: t.display, Average: t.average(now), Peak: t.peak}
	}

	instant := float64(totalBytes-t.anchorBytes) / dt
	t.instant = instant
	t.anchorAt = now
	t.anchorBytes = totalBytes
	t.activeAt = now
	t.appendWindow(now, totalBytes)

	sample := t.blendInstant(instant, now)
	sample = t.capSpike(sample)
	sample = t.applyWarmup(sample, now)

	if now.Before(t.cooldown) && t.display > 0 {
		return Sample{Instant: instant, Display: t.display, Average: t.average(now), Peak: t.peak}
	}

	if t.display <= 0 && sample > 0 {
		t.display = sample
		if t.display > t.peak {
			t.peak = t.display
		}
		return Sample{
			Instant: instant,
			Display: t.display,
			Average: t.average(now),
			Peak:    t.peak,
		}
	}

	t.display = t.smoothDisplay(sample)
	if t.display > t.peak {
		t.peak = t.display
	}

	return Sample{
		Instant: instant,
		Display: t.display,
		Average: t.average(now),
		Peak:    t.peak,
	}
}

func (t *Tracker) appendWindow(now time.Time, totalBytes int64) {
	t.window = append(t.window, windowPoint{at: now, bytes: totalBytes})
	cutoff := now.Add(-t.cfg.WindowDuration)
	for len(t.window) > 1 && t.window[0].at.Before(cutoff) {
		t.window = t.window[1:]
	}
}

func (t *Tracker) windowSpeed(now time.Time) float64 {
	if len(t.window) < 2 {
		return 0
	}
	first := t.window[0]
	last := t.window[len(t.window)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 || last.bytes <= first.bytes {
		return 0
	}
	return float64(last.bytes-first.bytes) / dt
}

func (t *Tracker) blendInstant(instant float64, now time.Time) float64 {
	window := t.windowSpeed(now)
	if window <= 0 {
		return instant
	}
	return window*0.8 + instant*0.2
}

func (t *Tracker) capSpike(sample float64) float64 {
	if t.display <= 0 || sample <= 0 {
		return sample
	}
	if sample <= t.display*t.cfg.SpikeRatio {
		return sample
	}
	excess := sample - t.display*t.cfg.SpikeRatio
	return t.display*t.cfg.SpikeRatio + excess*0.35
}

func (t *Tracker) applyWarmup(sample float64, now time.Time) float64 {
	if now.Sub(t.startedAt) >= t.cfg.WarmupDuration {
		return sample
	}
	avg := t.average(now)
	if avg <= 0 {
		return sample
	}
	return avg*0.35 + sample*0.65
}

func (t *Tracker) smoothDisplay(sample float64) float64 {
	if t.display <= 0 {
		return sample
	}
	if sample > t.display*t.cfg.RiseCapRatio {
		sample = t.display + (sample-t.display)*0.75
	}
	return t.display*(1-t.cfg.EMAAlpha) + sample*t.cfg.EMAAlpha
}

func (t *Tracker) decayDisplay(now time.Time, force bool) Sample {
	if t.display <= 0 {
		t.instant = 0
		return Sample{Display: 0, Average: t.average(now), Peak: t.peak}
	}
	activeAt := t.activeAt
	if activeAt.IsZero() {
		activeAt = t.anchorAt
	}
	idleFor := now.Sub(activeAt)
	if !force && idleFor < t.cfg.IdleGrace {
		return Sample{Instant: 0, Display: t.display, Average: t.average(now), Peak: t.peak}
	}
	factor := 1.0
	if force {
		factor = 0.25
	} else {
		decayFor := idleFor - t.cfg.IdleGrace
		if decayFor <= 0 {
			return Sample{Instant: 0, Display: t.display, Average: t.average(now), Peak: t.peak}
		}
		factor = 1 - decayFor.Seconds()/t.cfg.IdleDecay.Seconds()
		if factor < 0 {
			factor = 0
		}
	}
	t.display *= factor
	if t.display < 32*1024 {
		t.display = 0
	}
	t.instant = 0
	t.anchorAt = now
	t.anchorBytes = t.totalBytes
	t.appendWindow(now, t.totalBytes)
	return Sample{Instant: 0, Display: t.display, Average: t.average(now), Peak: t.peak}
}

func (t *Tracker) average(now time.Time) float64 {
	if t.startedAt.IsZero() || t.totalBytes <= 0 {
		return 0
	}
	elapsed := now.Sub(t.startedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(t.totalBytes) / elapsed
}

var slicePhasePattern = regexp.MustCompile(`分片[（(]\s*(\d+)\s*/\s*(\d+)\s*[)）]`)

func isSlicePhase(key string) bool {
	return strings.HasPrefix(key, "slice:")
}

func isActiveTransferPhase(key string) bool {
	return key == "transfer" || strings.HasSuffix(key, ":transfer") || isSlicePhase(key)
}

func PhaseKey(message string) string {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "default"
	}
	if m := slicePhasePattern.FindStringSubmatch(msg); len(m) == 3 {
		return "transfer"
	}
	if isPrepMessage(msg) {
		return "prep"
	}
	if strings.Contains(msg, "上传") || strings.Contains(msg, "下载") {
		return "transfer"
	}
	return "default"
}

func isPrepMessage(msg string) bool {
	if slicePhasePattern.MatchString(msg) {
		return false
	}
	for _, marker := range []string{
		"计算", "校验", "准备", "凭证", "预上传", "检查", "写入网盘", "成功", "完成",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
