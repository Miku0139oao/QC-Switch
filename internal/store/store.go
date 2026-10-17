package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"qc-switch/internal/domain"
	"qc-switch/internal/secure"
)

type Store struct {
	db  *sql.DB
	mu  sync.Mutex
	Dir string
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "qc-switch.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
	CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY,v BLOB NOT NULL);
	CREATE TABLE IF NOT EXISTS profiles(id TEXT PRIMARY KEY,body BLOB NOT NULL,secret BLOB);
	CREATE TABLE IF NOT EXISTS journals(client TEXT PRIMARY KEY,body BLOB NOT NULL);
	CREATE TABLE IF NOT EXISTS records(id INTEGER PRIMARY KEY AUTOINCREMENT,ts TEXT NOT NULL,body BLOB NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Dir: dir}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Put(k string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO kv VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", k, b)
	return err
}
func (s *Store) Get(k string, v any) error {
	var b []byte
	err := s.db.QueryRow("SELECT v FROM kv WHERE k=?", k).Scan(&b)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) Profiles() ([]domain.Profile, error) {
	rows, err := s.db.Query("SELECT body,secret IS NOT NULL FROM profiles ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Profile{}
	for rows.Next() {
		var b []byte
		var has bool
		if err = rows.Scan(&b, &has); err != nil {
			return nil, err
		}
		var p domain.Profile
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		p.HasKey = has
		p.Key = ""
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Profile(id string) (domain.Profile, error) {
	var b, key []byte
	var p domain.Profile
	err := s.db.QueryRow("SELECT body,secret FROM profiles WHERE id=?", id).Scan(&b, &key)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	p.HasKey = len(key) > 0
	if len(key) > 0 {
		plain, e := secure.Unprotect(key)
		if e != nil {
			return p, e
		}
		p.Key = string(plain)
	}
	return p, nil
}
func (s *Store) SaveProfile(p domain.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := p.Key
	p.Key = ""
	p.HasKey = false
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if key != "" {
		secret, e := secure.Protect([]byte(key))
		if e != nil {
			return e
		}
		_, err = s.db.Exec("INSERT INTO profiles VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,secret=excluded.secret", p.ID, b, secret)
	} else {
		_, err = s.db.Exec("INSERT INTO profiles(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", p.ID, b)
	}
	return err
}
func (s *Store) DeleteProfile(id string) error {
	_, e := s.db.Exec("DELETE FROM profiles WHERE id=?", id)
	return e
}

// Journals are encrypted as a unit, including original credentials and patches.
func (s *Store) SaveJournal(client string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	enc, e := secure.Protect(b)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("INSERT INTO journals VALUES(?,?) ON CONFLICT(client) DO UPDATE SET body=excluded.body", client, enc)
	return e
}
func (s *Store) Journal(client string, v any) error {
	var b []byte
	e := s.db.QueryRow("SELECT body FROM journals WHERE client=?", client).Scan(&b)
	if e != nil {
		return e
	}
	plain, e := secure.Unprotect(b)
	if e != nil {
		return e
	}
	return json.Unmarshal(plain, v)
}
func (s *Store) DeleteJournal(client string) error {
	_, e := s.db.Exec("DELETE FROM journals WHERE client=?", client)
	return e
}
func (s *Store) JournalClients() ([]string, error) {
	rows, e := s.db.Query("SELECT client FROM journals")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if e = rows.Scan(&c); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) AddRecord(r domain.Record) error {
	r.Time = time.Now().UTC()
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("INSERT INTO records(ts,body) VALUES(?,?)", r.Time.Format(time.RFC3339Nano), b)
	return e
}
func (s *Store) Records(limit int) ([]domain.Record, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, e := s.db.Query("SELECT id,body FROM records ORDER BY id DESC LIMIT ?", limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Record{}
	for rows.Next() {
		var r domain.Record
		var b []byte
		if e = rows.Scan(&r.ID, &b); e != nil {
			return nil, e
		}
		id := r.ID
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, e
		}
		r.ID = id
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Prune(days int) error {
	if days < 1 {
		return fmt.Errorf("retention must be positive")
	}
	_, e := s.db.Exec("DELETE FROM records WHERE ts < ?", time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) ClearRecords() error { _, e := s.db.Exec("DELETE FROM records"); return e }
