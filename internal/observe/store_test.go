package observe

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
