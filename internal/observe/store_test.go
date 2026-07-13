package observe

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBoundedStoreAndJSONL(t *testing.T) {
	s := NewStore(2)
	file := filepath.Join(t.TempDir(), "logs", "requests.jsonl")
	s.SetFile(file)
	s.Add(Record{ID: "1"})
	s.Add(Record{ID: "2"})
	s.Add(Record{ID: "3"})
	records := s.List(10)
	if len(records) != 2 || records[0].ID != "3" || records[1].ID != "2" {
		t.Fatalf("unexpected ring contents %#v", records)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte("\n")) != 3 {
		t.Fatalf("unexpected JSONL %s", raw)
	}
}

func TestConfigureReloadsPersistedJSONL(t *testing.T) {
	file := filepath.Join(t.TempDir(), "requests.jsonl")
	first := NewStore(3)
	first.Configure(3, file)
	first.Add(Record{ID: "persisted-1", StartedAt: time.Now().Add(-time.Second)})
	first.Add(Record{ID: "persisted-2", StartedAt: time.Now()})

	reloaded := NewStore(3)
	reloaded.Configure(3, file)
	records := reloaded.List(10)
	if len(records) != 2 || records[0].ID != "persisted-2" || records[1].ID != "persisted-1" {
		t.Fatalf("persisted records were not reloaded: %#v", records)
	}
}

func TestStoreConfigureResizesAndPreservesNewest(t *testing.T) {
	s := NewStore(3)
	s.Add(Record{ID: "1"})
	s.Add(Record{ID: "2"})
	s.Add(Record{ID: "3"})
	s.Configure(2, "")
	records := s.List(10)
	if len(records) != 2 || records[0].ID != "3" || records[1].ID != "2" {
		t.Fatalf("resize did not preserve newest records: %#v", records)
	}
	s.Configure(4, "")
	s.Add(Record{ID: "4"})
	records = s.List(10)
	if len(records) != 3 || records[0].ID != "4" || records[2].ID != "2" {
		t.Fatalf("grow did not preserve ring order: %#v", records)
	}
}
