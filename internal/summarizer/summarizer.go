package summarizer

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/yamd1/kioku/internal/embedding"
)

const minCharsToSummarize = 300

// Summarizer は埋め込みモデルを使った抽出要約を行う。
// 各文を embedding 化し、重心に近い代表文を選ぶ。
type Summarizer struct {
	embedder *embedding.Embedder
}

func New(embedder *embedding.Embedder) *Summarizer {
	return &Summarizer{embedder: embedder}
}

// Summarize はテキストから代表的な文を抽出して返す。
// テキストが短い場合や文が少ない場合は元テキストをそのまま返す。
// embedding エラー時も元テキストにフォールバックする。
func (s *Summarizer) Summarize(content string) (string, error) {
	if len([]rune(content)) < minCharsToSummarize {
		return content, nil
	}

	sentences := splitSentences(content)
	if len(sentences) <= 3 {
		return content, nil
	}

	embeddings := make([][]float32, len(sentences))
	for i, sent := range sentences {
		emb, err := s.embedder.Embed(sent)
		if err != nil {
			return content, nil // embedding 失敗時はそのまま返す
		}
		embeddings[i] = emb
	}

	centroid := centroidOf(embeddings)

	type scored struct {
		text  string
		score float64
		idx   int
	}
	items := make([]scored, len(sentences))
	for i, sent := range sentences {
		items[i] = scored{
			text:  sent,
			score: cosineSimilarity(centroid, embeddings[i]),
			idx:   i,
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	// 上位 40%（最低 3 文）を選ぶ
	topN := len(sentences) * 2 / 5
	if topN < 3 {
		topN = 3
	}
	if topN > len(items) {
		topN = len(items)
	}

	selected := items[:topN]

	// 元の順序に戻して自然な文章にする
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].idx < selected[j].idx
	})

	var sb strings.Builder
	for i, item := range selected {
		sb.WriteString(item.text)
		if i < len(selected)-1 {
			sb.WriteString(" ")
		}
	}
	return sb.String(), nil
}

// splitSentences は日英混在テキストを文単位で分割する。
func splitSentences(text string) []string {
	var sentences []string
	var buf strings.Builder

	runes := []rune(text)
	for i, r := range runes {
		buf.WriteRune(r)

		isEnd := r == '。' || r == '！' || r == '？' || r == '!' || r == '?'
		isDot := r == '.'
		isNewline := r == '\n'

		if isEnd || isNewline {
			s := strings.TrimSpace(buf.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			buf.Reset()
			continue
		}

		// 英語の . は次の文字が大文字か空白+大文字の場合のみ文末とみなす
		if isDot && i+1 < len(runes) {
			next := runes[i+1]
			if next == ' ' && i+2 < len(runes) && unicode.IsUpper(runes[i+2]) {
				s := strings.TrimSpace(buf.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				buf.Reset()
			}
		}
	}

	if s := strings.TrimSpace(buf.String()); s != "" {
		sentences = append(sentences, s)
	}

	return sentences
}

func centroidOf(embeddings [][]float32) []float32 {
	if len(embeddings) == 0 {
		return nil
	}
	dim := len(embeddings[0])
	centroid := make([]float32, dim)
	for _, emb := range embeddings {
		for i, v := range emb {
			centroid[i] += v
		}
	}
	n := float32(len(embeddings))
	for i := range centroid {
		centroid[i] /= n
	}
	return centroid
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
