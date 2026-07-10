package terminal

import (
	"encoding/json"
	"testing"
)

func TestMergeStoreJSON_PreservesUnmentionedKeys(t *testing.T) {
	base := json.RawMessage(`{"history":["a","b"],"remotePeers":[{"id":"p1"}]}`)
	patch := json.RawMessage(`{"history":["a","b","c"]}`) // client knows only history

	var m map[string]json.RawMessage
	if err := json.Unmarshal(mergeStoreJSON(base, patch), &m); err != nil {
		t.Fatalf("merge produced invalid json: %v", err)
	}
	// remotePeers must survive a history-only write — the whole point of the merge.
	if _, ok := m["remotePeers"]; !ok {
		t.Errorf("remotePeers wiped by a history-only patch — merge failed")
	}
	var hist []string
	if err := json.Unmarshal(m["history"], &hist); err != nil || len(hist) != 3 || hist[2] != "c" {
		t.Errorf("history not updated: %v (err %v)", hist, err)
	}
}

func TestMergeStoreJSON_EmptyBaseAndNonObject(t *testing.T) {
	// nil base → just the patch's keys.
	if got := string(mergeStoreJSON(nil, json.RawMessage(`{"history":["x"]}`))); got != `{"history":["x"]}` {
		t.Errorf("nil base: got %s", got)
	}
	// A non-object patch degrades to a plain replace rather than crashing/dropping the write.
	if got := string(mergeStoreJSON(json.RawMessage(`{"a":1}`), json.RawMessage(`[1,2,3]`))); got != `[1,2,3]` {
		t.Errorf("non-object patch: got %s", got)
	}
	// A corrupt base is treated as empty; the patch still lands.
	if got := string(mergeStoreJSON(json.RawMessage(`not json`), json.RawMessage(`{"k":1}`))); got != `{"k":1}` {
		t.Errorf("corrupt base: got %s", got)
	}
}

// The exact restart-time race that used to lose data: a fresh process (storeData==nil) receives a
// PARTIAL PUT (only history) before any GET hydrated the cache. handleSaveStore must merge onto the
// ON-DISK store so remotePeers survives.
func TestStoreSave_RestartPartialPutDoesNotClobberDisk(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	s.saveStoreToDisk(json.RawMessage(`{"history":["h1","h2"],"remotePeers":[{"id":"p1"}]}`))

	storeMu.Lock()
	storeData = nil // fresh process after restart
	storeMu.Unlock()
	t.Cleanup(func() { storeMu.Lock(); storeData = nil; storeMu.Unlock() })

	// Mirror handleSaveStore's nil-cache path.
	base := s.loadStoreFromDisk()
	merged := mergeStoreJSON(base, json.RawMessage(`{"history":["h1","h2","h3"]}`))
	s.saveStoreToDisk(merged)

	var m map[string]json.RawMessage
	if err := json.Unmarshal(s.loadStoreFromDisk(), &m); err != nil {
		t.Fatalf("reload disk: %v", err)
	}
	if _, ok := m["remotePeers"]; !ok {
		t.Errorf("remotePeers wiped from disk by a partial PUT after restart: %s", merged)
	}
	var hist []string
	json.Unmarshal(m["history"], &hist) //nolint:errcheck
	if len(hist) != 3 {
		t.Errorf("history not updated on disk: %v", hist)
	}
}
