package storage

import (
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type Memory struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
}

type MemoryChunk struct {
	ChunkIdx  int
	Text      string
	Embedding []float32
}

type SearchResult struct {
	Memory      *Memory
	Score       float64
	VectorScore float64
	MatchedChunk string
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
	// memories テーブル（embeddingカラムは旧スキーマ互換のため残す可能性あり）
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id         TEXT PRIMARY KEY,
			content    TEXT NOT NULL,
			source     TEXT NOT NULL DEFAULT '',
			tags       TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at DESC);
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_chunks (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			chunk_idx  INTEGER NOT NULL,
			text       TEXT NOT NULL,
			embedding  BLOB
		);
		CREATE INDEX IF NOT EXISTS idx_memory_chunks_memory_id ON memory_chunks(memory_id);
	`)
	return err
}

func (s *Store) Add(content, source string, tags []string) (*Memory, error) {
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	now := time.Now()

	_, err = s.db.Exec(
		`INSERT INTO memories (id, content, source, tags, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, content, source, string(tagsJSON), now,
	)
	if err != nil {
		return nil, err
	}

	return &Memory{
		ID:        id,
		Content:   content,
		Source:    source,
		Tags:      tags,
		CreatedAt: now,
	}, nil
}

func (s *Store) AddChunks(memoryID string, chunks []MemoryChunk) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for _, c := range chunks {
		_, err := tx.Exec(
			`INSERT INTO memory_chunks (memory_id, chunk_idx, text, embedding) VALUES (?, ?, ?, ?)`,
			memoryID, c.ChunkIdx, c.Text, float32SliceToBytes(c.Embedding),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Search(queryEmb []float32, query string, n int) ([]*SearchResult, error) {
	rows, err := s.db.Query(`
		SELECT mc.chunk_idx, mc.text, mc.embedding,
		       m.id, m.content, m.source, m.tags, m.created_at
		FROM memory_chunks mc
		JOIN memories m ON mc.memory_id = m.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type chunkHit struct {
		memoryID    string
		chunkText   string
		vectorScore float64
		content     string
		source      string
		tags        []string
		createdAt   time.Time
	}

	// memory_id ごとに最高スコアのチャンクを保持
	type memBest struct {
		hit  chunkHit
		best float64
	}
	bestByMem := map[string]*memBest{}

	for rows.Next() {
		var chunkIdx int
		var chunkText string
		var embBytes []byte
		var memID, content, source, tagsJSON string
		var createdAt time.Time

		if err := rows.Scan(&chunkIdx, &chunkText, &embBytes, &memID, &content, &source, &tagsJSON, &createdAt); err != nil {
			return nil, err
		}

		emb := bytesToFloat32Slice(embBytes)
		score := cosineSimilarity(queryEmb, emb)

		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			tags = []string{}
		}

		hit := chunkHit{
			memoryID:    memID,
			chunkText:   chunkText,
			vectorScore: score,
			content:     content,
			source:      source,
			tags:        tags,
			createdAt:   createdAt,
		}

		if mb, ok := bestByMem[memID]; !ok || score > mb.best {
			bestByMem[memID] = &memBest{hit: hit, best: score}
		}
	}

	now := time.Now()
	queryWords := strings.Fields(strings.ToLower(query))

	type finalEntry struct {
		result *SearchResult
		score  float64
	}
	finals := make([]finalEntry, 0, len(bestByMem))

	for memID, mb := range bestByMem {
		h := mb.hit
		keywordScore := computeKeywordScore(h.content, queryWords)
		recencyScore := computeRecencyScore(h.createdAt, now)
		score := 0.7*h.vectorScore + 0.2*keywordScore + 0.1*recencyScore

		finals = append(finals, finalEntry{
			result: &SearchResult{
				Memory: &Memory{
					ID:        memID,
					Content:   h.content,
					Source:    h.source,
					Tags:      h.tags,
					CreatedAt: h.createdAt,
				},
				Score:        score,
				VectorScore:  h.vectorScore,
				MatchedChunk: h.chunkText,
			},
			score: score,
		})
	}

	sort.Slice(finals, func(i, j int) bool {
		return finals[i].score > finals[j].score
	})

	if n > len(finals) {
		n = len(finals)
	}

	out := make([]*SearchResult, n)
	for i := range out {
		out[i] = finals[i].result
	}
	return out, nil
}

func (s *Store) Recent(n int, source string) ([]*Memory, error) {
	var rows *sql.Rows
	var err error

	if source != "" {
		rows, err = s.db.Query(
			`SELECT id, content, source, tags, created_at FROM memories WHERE source = ? ORDER BY created_at DESC LIMIT ?`,
			source, n,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, content, source, tags, created_at FROM memories ORDER BY created_at DESC LIMIT ?`,
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

// ChunkText はテキストを 300〜500 トークン相当のチャンクに分割する。
// 1トークン ≈ 4文字として近似。overlap は 50 トークン相当。
func ChunkText(text string) []string {
	const (
		targetChars  = 1600 // ~400 tokens
		overlapChars = 200  // ~50 tokens
		minChars     = 1200 // ~300 tokens
	)

	runes := []rune(text)
	if len(runes) <= targetChars {
		return []string{text}
	}

	var chunks []string
	start := 0

	for start < len(runes) {
		end := start + targetChars
		if end >= len(runes) {
			chunks = append(chunks, string(runes[start:]))
			break
		}

		// 文境界で切る（。\n.!? など）
		breakAt := end
		for i := end; i > start+minChars; i-- {
			r := runes[i]
			if r == '。' || r == '\n' || r == '.' || r == '!' || r == '?' {
				breakAt = i + 1
				break
			}
		}

		chunks = append(chunks, string(runes[start:breakAt]))

		next := breakAt - overlapChars
		if next <= start {
			next = start + 1 // 無限ループ防止
		}
		start = next
	}
	return chunks
}

func scanMemory(rows *sql.Rows) (*Memory, error) {
	var m Memory
	var tagsJSON string

	if err := rows.Scan(&m.ID, &m.Content, &m.Source, &tagsJSON, &m.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
		m.Tags = []string{}
	}
	return &m, nil
}

func computeKeywordScore(content string, queryWords []string) float64 {
	if len(queryWords) == 0 {
		return 0
	}
	lower := strings.ToLower(content)
	matches := 0
	for _, w := range queryWords {
		if strings.Contains(lower, w) {
			matches++
		}
	}
	return float64(matches) / float64(len(queryWords))
}

func computeRecencyScore(createdAt, now time.Time) float64 {
	age := now.Sub(createdAt)
	days := age.Hours() / 24
	// 指数減衰: day0=1.0, day365≈0.37
	return math.Exp(-days / 365)
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
