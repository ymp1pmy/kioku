package storage

import (
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type Memory struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	Tags      []string  `json:"tags"`
	Embedding []float32 `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id         TEXT PRIMARY KEY,
			content    TEXT NOT NULL,
			source     TEXT NOT NULL DEFAULT '',
			tags       TEXT NOT NULL DEFAULT '[]',
			embedding  BLOB,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at DESC);
	`)
	return err
}

func (s *Store) Add(content, source string, tags []string, embedding []float32) (*Memory, error) {
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}

	embBytes := float32SliceToBytes(embedding)
	id := uuid.New().String()
	now := time.Now()

	_, err = s.db.Exec(
		`INSERT INTO memories (id, content, source, tags, embedding, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, content, source, string(tagsJSON), embBytes, now,
	)
	if err != nil {
		return nil, err
	}

	return &Memory{
		ID:        id,
		Content:   content,
		Source:    source,
		Tags:      tags,
		Embedding: embedding,
		CreatedAt: now,
	}, nil
}

func (s *Store) Search(queryEmb []float32, n int) ([]*Memory, error) {
	rows, err := s.db.Query(`SELECT id, content, source, tags, embedding, created_at FROM memories`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		mem   *Memory
		score float64
	}
	var results []scored

	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		score := cosineSimilarity(queryEmb, m.Embedding)
		results = append(results, scored{m, score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if n > len(results) {
		n = len(results)
	}

	out := make([]*Memory, n)
	for i := range out {
		out[i] = results[i].mem
	}
	return out, nil
}

func (s *Store) Recent(n int, source string) ([]*Memory, error) {
	var rows *sql.Rows
	var err error

	if source != "" {
		rows, err = s.db.Query(
			`SELECT id, content, source, tags, embedding, created_at FROM memories WHERE source = ? ORDER BY created_at DESC LIMIT ?`,
			source, n,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, content, source, tags, embedding, created_at FROM memories ORDER BY created_at DESC LIMIT ?`,
			n,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanMemory(rows *sql.Rows) (*Memory, error) {
	var m Memory
	var tagsJSON string
	var embBytes []byte

	if err := rows.Scan(&m.ID, &m.Content, &m.Source, &tagsJSON, &embBytes, &m.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
		m.Tags = []string{}
	}
	m.Embedding = bytesToFloat32Slice(embBytes)
	return &m, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func float32SliceToBytes(f []float32) []byte {
	b := make([]byte, len(f)*4)
	for i, v := range f {
		bits := math.Float32bits(v)
		b[i*4] = byte(bits)
		b[i*4+1] = byte(bits >> 8)
		b[i*4+2] = byte(bits >> 16)
		b[i*4+3] = byte(bits >> 24)
	}
	return b
}

func bytesToFloat32Slice(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	f := make([]float32, len(b)/4)
	for i := range f {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		f[i] = math.Float32frombits(bits)
	}
	return f
}
