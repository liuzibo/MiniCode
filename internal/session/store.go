package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ssbsunshengbo/minicode-go/internal/message"
)

type Store struct {
	Dir string
}

type Record struct {
	ID        string            `json:"id"`
	CWD       string            `json:"cwd,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Messages  []message.Message `json:"messages"`
}

type Summary struct {
	ID           string
	CWD          string
	UpdatedAt    time.Time
	MessageCount int
}

func NewRecord(cwd string, messages []message.Message) Record {
	now := time.Now().UTC()
	return Record{
		ID:        now.Format("20060102-150405"),
		CWD:       cwd,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  append([]message.Message(nil), messages...),
	}
}

func (s Store) Save(record Record) error {
	if strings.TrimSpace(record.ID) == "" {
		record.ID = time.Now().UTC().Format("20060102-150405")
	}
	path, err := s.path(record.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(bytes, '\n'), 0o644)
}

func (s Store) Load(id string) (Record, error) {
	path, err := s.path(id)
	if err != nil {
		return Record{}, err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(bytes, &record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s Store) Latest() (Record, error) {
	summaries, err := s.List()
	if err != nil {
		return Record{}, err
	}
	if len(summaries) == 0 {
		return Record{}, fmt.Errorf("no saved sessions")
	}
	return s.Load(summaries[0].ID)
}

func (s Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := s.Load(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, Summary{ID: record.ID, CWD: record.CWD, UpdatedAt: record.UpdatedAt, MessageCount: len(record.Messages)})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s Store) path(id string) (string, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return "", fmt.Errorf("session store directory is empty")
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("session id is empty")
	}
	if id != filepath.Base(id) || strings.Contains(id, string(filepath.Separator)) {
		return "", fmt.Errorf("invalid session id: %s", id)
	}
	return filepath.Join(s.Dir, id+".json"), nil
}
