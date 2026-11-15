package biz

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A source is the content owner, a channel is one web entry of that source.
// Both are configuration an operator edits and pauses; the plan still names the
// (source_code, channel) pair, which is what the worker already uses.

// Statuses of a source and of a channel.
const (
	SourceStatusEnabled = "enabled"
	SourceStatusPaused  = "paused"
)

// Bounds of the configuration form.
const (
	SourceNameLimit   = 80
	ChannelNameLimit  = 80
	ChannelRateMin    = 1
	ChannelRateMax    = 600
	ChannelTimeoutMin = 5
	ChannelTimeoutMax = 120
)

// Configuration failures.
var (
	ErrSourceNotFound    = errors.New("source not found")
	ErrSourceVersion     = errors.New("source version conflict")
	ErrSourceName        = errors.New("the source name is required and must fit the limit")
	ErrSourceStatus      = errors.New("the source status is not valid")
	ErrChannelNotFound   = errors.New("channel not found")
	ErrChannelVersion    = errors.New("channel version conflict")
	ErrChannelName       = errors.New("the channel name is required and must fit the limit")
	ErrChannelEntry      = errors.New("the channel entry is not supported by the adapter")
	ErrChannelURL        = errors.New("the channel url is not allowed")
	ErrChannelRate       = errors.New("the channel rate is out of range")
	ErrChannelTimeout    = errors.New("the channel timeout is out of range")
	ErrChannelAdapter    = errors.New("the channel adapter is not registered")
	ErrSourcePaused      = errors.New("the source or channel is paused")
	ErrChannelProbeUsage = errors.New("the channel probe needs an enabled channel")
)

// SourceEntry is one entry an adapter declares. The console offers exactly
// these, and the server stores nothing else.
type SourceEntry struct {
	SourceCode string
	AdapterKey string
	// Kind is the entry kind; only "rank" is connected.
	Kind string
	// Channel is the concrete slug the adapter builds its request from.
	Channel string
	Label   string
	// Description explains what the entry reads.
	Description string
	BaseURL     string
	// AllowedHosts are the hosts the adapter may talk to for this entry.
	AllowedHosts []string
}

// SourceConfig is one source row.
type SourceConfig struct {
	ID        int64
	Code      string
	Name      string
	Status    string
	Version   int
	UpdatedAt time.Time
}

// ChannelConfig is one channel row with its source.
type ChannelConfig struct {
	ID            int64
	SourceID      int64
	SourceCode    string
	SourceName    string
	SourceStatus  string
	Code          string
	Name          string
	AdapterKey    string
	EntryKind     string
	EntryCode     string
	BaseURL       string
	AllowedHosts  string
	RatePerMinute int
	TimeoutSecond int
	// SecretRef only names a private server configuration.
	SecretRef   string
	HasSecret   bool
	Status      string
	Version     int
	UpdatedAt   time.Time
	Effective   bool
	Paused      bool
	PausedCause string
}

// ChannelUpdate is the editable part of a channel.
type ChannelUpdate struct {
	Name          string
	EntryKind     string
	EntryCode     string
	BaseURL       string
	AllowedHosts  string
	RatePerMinute int
	TimeoutSecond int
	SecretRef     string
}

// ChannelProbe is the result of an explicit connection test.
type ChannelProbe struct {
	OK           bool
	Channel      string
	LatencyMS    int64
	Items        int
	TotalPages   int
	ErrorCode    string
	ErrorMessage string
	ProbedAt     time.Time
}

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// NormalizeSourceName trims and bounds a source name.
func NormalizeSourceName(name string) (string, error) {
	out := strings.TrimSpace(name)
	if out == "" || len([]rune(out)) > SourceNameLimit {
		return "", fmt.Errorf("%w: 1~%d characters", ErrSourceName, SourceNameLimit)
	}
	return out, nil
}

// ValidSourceStatus accepts only the two documented statuses.
func ValidSourceStatus(status string) error {
	if status != SourceStatusEnabled && status != SourceStatusPaused {
		return fmt.Errorf("%w: %q", ErrSourceStatus, status)
	}
	return nil
}

// NormalizeChannelUpdate trims the text and checks every bound before any
// write. The entry is checked against the adapter by ValidateChannelEntry.
func NormalizeChannelUpdate(in ChannelUpdate) (ChannelUpdate, error) {
	out := ChannelUpdate{
		Name:          strings.TrimSpace(in.Name),
		EntryKind:     strings.TrimSpace(in.EntryKind),
		EntryCode:     strings.TrimSpace(in.EntryCode),
		BaseURL:       strings.TrimSpace(in.BaseURL),
		RatePerMinute: in.RatePerMinute,
		TimeoutSecond: in.TimeoutSecond,
		SecretRef:     strings.TrimSpace(in.SecretRef),
	}
	if out.Name == "" || len([]rune(out.Name)) > ChannelNameLimit {
		return ChannelUpdate{}, fmt.Errorf("%w: 1~%d characters", ErrChannelName, ChannelNameLimit)
	}
	if out.RatePerMinute < ChannelRateMin || out.RatePerMinute > ChannelRateMax {
		return ChannelUpdate{}, fmt.Errorf("%w: %d~%d", ErrChannelRate, ChannelRateMin, ChannelRateMax)
	}
	if out.TimeoutSecond < ChannelTimeoutMin || out.TimeoutSecond > ChannelTimeoutMax {
		return ChannelUpdate{}, fmt.Errorf("%w: %d~%d", ErrChannelTimeout, ChannelTimeoutMin, ChannelTimeoutMax)
	}
	hosts, err := NormalizeHosts(in.AllowedHosts)
	if err != nil {
		return ChannelUpdate{}, err
	}
	host, err := OriginHost(out.BaseURL)
	if err != nil {
		return ChannelUpdate{}, err
	}
	if !containsString(hosts, host) {
		return ChannelUpdate{}, fmt.Errorf("%w: the host allowlist must contain %s", ErrChannelURL, host)
	}
	out.AllowedHosts = strings.Join(hosts, ",")
	return out, nil
}

// NormalizeHosts lowercases and checks a comma separated host list.
func NormalizeHosts(raw string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		host := strings.ToLower(strings.TrimSpace(part))
		if host == "" {
			continue
		}
		if !hostPattern.MatchString(host) {
			return nil, fmt.Errorf("%w: %q is not a bare host name", ErrChannelURL, host)
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the host allowlist is empty", ErrChannelURL)
	}
	sort.Strings(out)
	return out, nil
}

// OriginHost returns the host of an HTTPS origin without path, credentials or
// query. It accepts the same shape the web adapter accepts.
func OriginHost(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("%w: %q must be an absolute HTTPS origin without credentials", ErrChannelURL, raw)
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: %q must not carry a path, query or fragment", ErrChannelURL, raw)
	}
	return strings.ToLower(parsed.Host), nil
}

// ValidateChannelEntry checks the adapter and the entry against what the
// server actually registered, and keeps the origin inside the declared hosts.
func ValidateChannelEntry(adapter WebMetadataAdapter, channel ChannelConfig) error {
	if adapter == nil {
		return fmt.Errorf("%w: %s", ErrChannelAdapter, channel.AdapterKey)
	}
	var entry *SourceEntry
	for _, candidate := range adapter.Entries() {
		if candidate.Kind == channel.EntryKind && candidate.Channel == channel.EntryCode {
			found := candidate
			entry = &found
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("%w: %s/%s", ErrChannelEntry, channel.EntryKind, channel.EntryCode)
	}
	host, err := OriginHost(channel.BaseURL)
	if err != nil {
		return err
	}
	if !containsString(entry.AllowedHosts, host) {
		return fmt.Errorf("%w: %s is not declared by the adapter", ErrChannelURL, host)
	}
	for _, host := range strings.Split(channel.AllowedHosts, ",") {
		if !containsString(entry.AllowedHosts, host) {
			return fmt.Errorf("%w: %s is not declared by the adapter", ErrChannelURL, host)
		}
	}
	return nil
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// ChannelPaused reports whether collection through this channel is stopped.
func (c ChannelConfig) ChannelPaused() bool {
	return c.Status == SourceStatusPaused || c.SourceStatus == SourceStatusPaused
}

// EffectiveRate is the rate a run uses: the plan override when it is set,
// otherwise the channel value.
func EffectiveRate(planRate, channelRate int) (rate int, source string) {
	if planRate > 0 {
		return planRate, "plan"
	}
	return channelRate, "channel"
}

// ListSources returns every source with its version.
func (uc *SourceCatalog) ListSources(ctx context.Context) ([]SourceConfig, error) {
	return uc.repo.ListSources(ctx)
}

// UpdateSource renames or pauses one source with a version check.
func (uc *SourceCatalog) UpdateSource(ctx context.Context, id int64, expectedVersion int, name, status string) (config *SourceConfig, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionSourceUpdate, AuditResourceSource)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	normalized, err := NormalizeSourceName(name)
	if err != nil {
		return nil, err
	}
	if status != "" {
		if err := ValidSourceStatus(status); err != nil {
			return nil, err
		}
	}
	return uc.repo.UpdateSource(ctx, id, expectedVersion, normalized, status)
}

// ListChannels returns every channel, optionally of one source.
func (uc *SourceCatalog) ListChannels(ctx context.Context, sourceID int64) ([]ChannelConfig, error) {
	return uc.repo.ListChannels(ctx, sourceID)
}

// GetChannel returns one channel.
func (uc *SourceCatalog) GetChannel(ctx context.Context, id int64) (*ChannelConfig, error) {
	return uc.repo.GetChannel(ctx, id)
}

// SourceEntries declares what the registered adapters support.
func (uc *SourceCatalog) SourceEntries() []SourceEntry {
	entries := make([]SourceEntry, 0, len(uc.adapters))
	for _, adapter := range uc.adapters {
		entries = append(entries, adapter.Entries()...)
	}
	return entries
}

// UpdateChannel edits the configuration with a version check. The adapter and
// the entry must be declared by the server, and the origin must stay inside
// the hosts that entry declares.
func (uc *SourceCatalog) UpdateChannel(ctx context.Context, id int64, expectedVersion int, update ChannelUpdate) (config *ChannelConfig, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionChannelUpdate, AuditResourceChannel)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	current, err := uc.repo.GetChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	normalized, err := NormalizeChannelUpdate(update)
	if err != nil {
		return nil, err
	}
	adapter := uc.adapterFor(current.AdapterKey)
	merged := *current
	merged.EntryKind = normalized.EntryKind
	merged.EntryCode = normalized.EntryCode
	merged.BaseURL = normalized.BaseURL
	merged.AllowedHosts = normalized.AllowedHosts
	if err := ValidateChannelEntry(adapter, merged); err != nil {
		return nil, err
	}
	return uc.repo.UpdateChannel(ctx, id, expectedVersion, normalized)
}

// SetChannelStatus enables or pauses one channel with a version check.
func (uc *SourceCatalog) SetChannelStatus(ctx context.Context, id int64, expectedVersion int, status string) (config *ChannelConfig, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionChannelStatus, AuditResourceChannel)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	if err := ValidSourceStatus(status); err != nil {
		return nil, err
	}
	return uc.repo.SetChannelStatus(ctx, id, expectedVersion, status)
}

// TestChannel runs one explicit, bounded request. Saving a configuration never
// calls the source; this does, under the channel's own timeout.
func (uc *SourceCatalog) TestChannel(ctx context.Context, id int64) (*ChannelProbe, error) {
	channel, err := uc.repo.GetChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	if channel.ChannelPaused() {
		return nil, ErrChannelProbeUsage
	}
	adapter := uc.adapterFor(channel.AdapterKey)
	if adapter == nil {
		return nil, fmt.Errorf("%w: %s", ErrChannelAdapter, channel.AdapterKey)
	}
	timeout := time.Duration(channel.TimeoutSecond) * time.Second
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	page, err := adapter.ListSeries(probeCtx, ListSeriesRequest{
		Channel: channel.EntryCode,
		Page:    1,
		Origin:  channel.BaseURL,
	})
	probe := &ChannelProbe{
		Channel:   channel.EntryCode,
		LatencyMS: time.Since(started).Milliseconds(),
		ProbedAt:  time.Now().UTC(),
	}
	if err != nil {
		if code, ok := SourceErrorCodeOf(err); ok {
			probe.ErrorCode = string(code)
			probe.ErrorMessage = maskProbeError(err)
		} else {
			probe.ErrorCode = "SOURCE_UNAVAILABLE"
			probe.ErrorMessage = "连接失败"
		}
		return probe, nil
	}
	probe.OK = true
	probe.Items = len(page.Items)
	probe.TotalPages = page.TotalPages
	return probe, nil
}

// maskProbeError keeps the stable code and drops anything that could leak the
// private configuration or a signed address.
func maskProbeError(err error) string {
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) && sourceErr.Err != nil {
		message := sourceErr.Err.Error()
		if len(message) > 160 {
			message = message[:160]
		}
		return message
	}
	return string(SourceErrorCodeUnavailable)
}

// SourceErrorCodeUnavailable is the fallback probe code.
const SourceErrorCodeUnavailable SourceErrorCode = SourceCodeUnavailable

func (uc *SourceCatalog) adapterFor(key string) WebMetadataAdapter {
	if adapter, ok := uc.adapters[key]; ok {
		return adapter
	}
	return nil
}
