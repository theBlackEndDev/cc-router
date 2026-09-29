package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// maxLogBytes caps decisions.jsonl; the previous file is kept as .1.
const maxLogBytes = 1 << 20

// ReadQuota loads <dataDir>/quota.json. Missing or unreadable means no data.
func ReadQuota(dataDir string) *Quota {
	if dataDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "quota.json"))
	if err != nil {
		return nil
	}
	var q Quota
	if json.Unmarshal(raw, &q) != nil {
		return nil
	}
	return &q
}

// WriteJSON writes v to <dataDir>/<name> atomically (temp file + rename), so a
// concurrent reader never sees a half-written file.
func WriteJSON(dataDir, name string, v any) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, "."+name+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dataDir, name))
}

// AppendLog appends one JSON line to <dataDir>/<name>, rotating at maxLogBytes.
func AppendLog(dataDir, name string, rec map[string]any) error {
	if dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dataDir, name)
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
	if _, ok := rec["ts"]; !ok {
		rec["ts"] = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
