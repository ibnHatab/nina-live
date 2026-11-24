// Package log is Nina's memory: one SQLite file, append only. Every event that happens to her
// is a row: what she heard and said, what the home's sensors read, what the rules concluded,
// what a task did. The world, the episodes, the tasks and the preferences are projections of
// it, rebuilt by reading it again (ARCHITECTURE.md §2).
//
// The seq orders the log; At is world time, the `now` the world's calls take; Stream groups
// the events (world, dialog, agent, task, prefs, sys); Data is the event's JSON.
package log

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

type Event struct {
	Seq    int64           `json:"seq"`
	At     float64         `json:"at"`
	Stream string          `json:"stream"`
	Kind   string          `json:"kind"`
	Key    string          `json:"key,omitempty"`
	Cause  string          `json:"cause,omitempty"`
	Data   json.RawMessage `json:"data"`
	V      int             `json:"v"`
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
CREATE TABLE IF NOT EXISTS events (
  seq    INTEGER PRIMARY KEY,
  at     REAL    NOT NULL,
  stream TEXT    NOT NULL,
  kind   TEXT    NOT NULL,
  key    TEXT,
  cause  TEXT,
  data   TEXT    NOT NULL,
  v      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS events_stream ON events (stream, seq);
CREATE VIRTUAL TABLE IF NOT EXISTS episodes USING fts5(text, kind, at UNINDEXED, seq UNINDEXED);
`

// Log is the open file. Append is safe from any goroutine; the subscribers see the events in
// seq order.
type Log struct {
	db   *sql.DB
	mu   sync.Mutex
	last int64
	subs []chan Event
}

// Open opens (or creates) the log at path.
func Open(path string) (*Log, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; SQLite serialises anyway, and the seq is ours
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("log %s: %w", path, err)
	}
	l := &Log{db: db}
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&l.last); err != nil {
		db.Close()
		return nil, err
	}
	return l, nil
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.subs {
		close(c)
	}
	l.subs = nil
	return l.db.Close()
}

// Last is the last seq written (0: empty).
func (l *Log) Last() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

// Append writes one event; data is marshalled to JSON. → the event as written (its seq).
func (l *Log) Append(at float64, stream, kind, key, cause string, data any, v int) (Event, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return Event{}, fmt.Errorf("log: %s %s: %w", stream, kind, err)
	}
	if v == 0 {
		v = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ev := Event{Seq: l.last + 1, At: at, Stream: stream, Kind: kind, Key: key, Cause: cause, Data: b, V: v}
	if _, err := l.db.Exec(`INSERT INTO events (seq, at, stream, kind, key, cause, data, v) VALUES (?,?,?,?,?,?,?,?)`,
		ev.Seq, ev.At, ev.Stream, ev.Kind, nullable(ev.Key), nullable(ev.Cause), string(ev.Data), ev.V); err != nil {
		return Event{}, fmt.Errorf("log: %w", err)
	}
	l.last = ev.Seq
	for _, c := range l.subs {
		select {
		case c <- ev:
		default: // a subscriber that cannot keep up misses live events; it reads them back with Read
		}
	}
	return ev, nil
}

// Episode indexes a text for recall (what was said, heard, done), pointing at its event.
func (l *Log) Episode(ev Event, text string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.Exec(`INSERT INTO episodes (text, kind, at, seq) VALUES (?,?,?,?)`, text, ev.Stream+"."+ev.Kind, ev.At, ev.Seq)
	return err
}

// Recall finds episodes matching an FTS5 query, the most recent first.
func (l *Log) Recall(ctx context.Context, query string, limit int) ([]Episode, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT seq, at, kind, text FROM episodes WHERE episodes MATCH ? ORDER BY seq DESC LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := rows.Scan(&e.Seq, &e.At, &e.Kind, &e.Text); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Episode struct {
	Seq  int64
	At   float64
	Kind string
	Text string
}

// Read calls f for every event after seq `from` in the streams named (none: all), in seq order.
// f returning an error stops the read with it.
func (l *Log) Read(ctx context.Context, from int64, f func(Event) error, streams ...string) error {
	q := `SELECT seq, at, stream, kind, COALESCE(key,''), COALESCE(cause,''), data, v FROM events WHERE seq > ?`
	args := []any{from}
	if len(streams) > 0 {
		q += ` AND stream IN (?` + repeat(",?", len(streams)-1) + `)`
		for _, s := range streams {
			args = append(args, s)
		}
	}
	rows, err := l.db.QueryContext(ctx, q+` ORDER BY seq`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var evs []Event // read all first: f may Append, and the one connection is ours
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.Seq, &e.At, &e.Stream, &e.Kind, &e.Key, &e.Cause, &data, &e.V); err != nil {
			return err
		}
		e.Data = json.RawMessage(data)
		evs = append(evs, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, e := range evs {
		if err := f(e); err != nil {
			return err
		}
	}
	return nil
}

// Subscribe is a channel of the events appended from now on (buffered; a slow reader misses
// events and reads them back with Read from the last seq it saw).
func (l *Log) Subscribe(buf int) <-chan Event {
	c := make(chan Event, buf)
	l.mu.Lock()
	l.subs = append(l.subs, c)
	l.mu.Unlock()
	return c
}

// ErrStop is returned by a Read callback to stop reading without an error.
var ErrStop = errors.New("stop")

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
