package observe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zbss/airoute/internal/protocol/ir"
)

type Attempt struct {
	Number     int    `json:"number"`
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Status     int    `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}
type Record struct {
	ID               string          `json:"id"`
	StartedAt        time.Time       `json:"started_at"`
	ClientProtocol   ir.Protocol     `json:"client_protocol"`
	ClientKeyID      string          `json:"client_key_id,omitempty"`
	ClientID         string          `json:"client_id,omitempty"`
	ClientName       string          `json:"client_name_snapshot,omitempty"`
	CredentialID     string          `json:"credential_id,omitempty"`
	CredentialPrefix string          `json:"credential_prefix,omitempty"`
	AuthSource       string          `json:"auth_source,omitempty"`
	RejectionReason  string          `json:"rejection_reason,omitempty"`
	ConfigVersion    string          `json:"config_version"`
	RequestedModel   string          `json:"requested_model"`
	RouteID          string          `json:"route_id"`
	ProviderID       string          `json:"provider_id"`
	UpstreamProtocol ir.Protocol     `json:"upstream_protocol,omitempty"`
	ResolvedModel    string          `json:"resolved_model"`
	Status           int             `json:"status"`
	DurationMS       int64           `json:"duration_ms"`
	FirstTokenMS     int64           `json:"first_token_ms,omitempty"`
	Usage            ir.Usage        `json:"usage"`
	ErrorCode        string          `json:"error_code,omitempty"`
	Diagnostics      []ir.Diagnostic `json:"diagnostics,omitempty"`
	DiagnosticCodes  []string        `json:"diagnostic_codes,omitempty"`
	Attempts         []Attempt       `json:"attempts,omitempty"`
	RequestBody      string          `json:"request_body,omitempty"`
	ResponseBody     string          `json:"response_body,omitempty"`
}
type Store struct {
	mu      sync.RWMutex
	records []Record
	cap     int
	next    int
	full    bool
	file    string
	loaded  string
}

func NewStore(capacity int) *Store {
	if capacity < 1 {
		capacity = 50
	}
	return &Store{records: make([]Record, capacity), cap: capacity}
}
func (s *Store) Add(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[s.next] = r
	s.next = (s.next + 1) % s.cap
	if s.next == 0 {
		s.full = true
	}
	s.writeFile(r)
}
func (s *Store) SetFile(path string) { s.mu.Lock(); defer s.mu.Unlock(); s.file = path }
func (s *Store) Configure(capacity int, file string) {
	if capacity < 1 {
		capacity = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if capacity != s.cap {
		recent := s.recordsLocked()
		if len(recent) > capacity {
			recent = recent[len(recent)-capacity:]
		}
		s.resetLocked(capacity, recent)
	}
	s.file = file
	if file == "" {
		s.loaded = ""
		return
	}
	if file != s.loaded {
		s.loadFilesLocked(file)
		s.loaded = file
	}
}

func (s *Store) recordsLocked() []Record {
	count := s.next
	if s.full {
		count = s.cap
	}
	out := make([]Record, 0, count)
	start := 0
	if s.full {
		start = s.next
	}
	for i := 0; i < count; i++ {
		out = append(out, s.records[(start+i)%s.cap])
	}
	return out
}

func (s *Store) resetLocked(capacity int, records []Record) {
	s.records = make([]Record, capacity)
	s.cap, s.next, s.full = capacity, 0, false
	for _, record := range records {
		s.records[s.next] = record
		s.next = (s.next + 1) % s.cap
		if s.next == 0 {
			s.full = true
		}
	}
}

func (s *Store) loadFilesLocked(path string) {
	records := s.recordsLocked()
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		seen[record.ID] = true
	}
	for _, candidate := range []string{path + ".3", path + ".2", path + ".1", path} {
		file, err := os.Open(candidate)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		for scanner.Scan() {
			var record Record
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.ID != "" && !seen[record.ID] {
				records = append(records, record)
				seen[record.ID] = true
			}
		}
		_ = file.Close()
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].StartedAt.Before(records[j].StartedAt) })
	if len(records) > s.cap {
		records = records[len(records)-s.cap:]
	}
	s.resetLocked(s.cap, records)
}
func (s *Store) writeFile(r Record) {
	if s.file == "" {
		return
	}
	dir := filepath.Dir(s.file)
	if dir != "." {
		_ = os.MkdirAll(dir, 0700)
	}
	if st, e := os.Stat(s.file); e == nil && st.Size() > 10<<20 {
		s.rotate()
	}
	f, e := os.OpenFile(s.file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	b, e := json.Marshal(r)
	if e == nil {
		_, _ = f.Write(append(b, '\n'))
	}
}
func (s *Store) rotate() {
	_ = os.Remove(s.file + ".3")
	_ = os.Rename(s.file+".2", s.file+".3")
	_ = os.Rename(s.file+".1", s.file+".2")
	_ = os.Rename(s.file, s.file+".1")
}
func (s *Store) List(limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.next
	if s.full {
		n = s.cap
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Record, 0, limit)
	for i := 0; i < limit; i++ {
		idx := (s.next - 1 - i + s.cap) % s.cap
		if !s.full && idx >= s.next {
			continue
		}
		out = append(out, s.records[idx])
	}
	return out
}
func (s *Store) Get(id string) (Record, bool) {
	for _, r := range s.List(s.cap) {
		if r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

type Metrics struct {
	Requests          atomic.Uint64
	Errors            atomic.Uint64
	InFlight          atomic.Int64
	Retries           atomic.Uint64
	Fallbacks         atomic.Uint64
	Timeouts          atomic.Uint64
	Cancellations     atomic.Uint64
	Diagnostics       atomic.Uint64
	InputTokens       atomic.Uint64
	OutputTokens      atomic.Uint64
	Completed         atomic.Uint64
	LatencyMSTotal    atomic.Uint64
	FirstTokenMSTotal atomic.Uint64
	LatencyBuckets    [7]atomic.Uint64
	FirstTokenBuckets [7]atomic.Uint64
	mu                sync.RWMutex
	series            map[string]MetricSeries
}

type MetricSeries struct {
	Protocol     string
	Provider     string
	Model        string
	Status       int
	Requests     uint64
	Errors       uint64
	LatencyMS    uint64
	FirstTokenMS uint64
}

func (m *Metrics) Record(protocol ir.Protocol, provider, model string, status int, latency, firstToken int64) {
	observeHistogram(&m.LatencyBuckets, latency)
	if firstToken > 0 {
		observeHistogram(&m.FirstTokenBuckets, firstToken)
	}
	key := string(protocol) + "\x00" + provider + "\x00" + model + "\x00" + fmt.Sprint(status)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.series == nil {
		m.series = map[string]MetricSeries{}
	}
	s := m.series[key]
	s.Protocol = string(protocol)
	s.Provider = provider
	s.Model = model
	s.Status = status
	s.Requests++
	if status >= 400 {
		s.Errors++
	}
	s.LatencyMS += uint64(max(int64(0), latency))
	s.FirstTokenMS += uint64(max(int64(0), firstToken))
	m.series[key] = s
}

var HistogramBounds = [...]int64{5, 10, 50, 100, 500, 1000}

func observeHistogram(buckets *[7]atomic.Uint64, value int64) {
	for i, bound := range HistogramBounds {
		if value <= bound {
			buckets[i].Add(1)
		}
	}
	buckets[len(buckets)-1].Add(1)
}
func (m *Metrics) Series() []MetricSeries {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MetricSeries, 0, len(m.series))
	for _, s := range m.series {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Status < out[j].Status
	})
	return out
}
