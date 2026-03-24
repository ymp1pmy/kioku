// ortgenai-stub は CGO に依存する knights-analytics/ortgenai の
// 純粋Goスタブ実装です。hugot.NewGoSession() のみ使用するため
// generative session 機能は未実装（すべてエラーを返します）。
package ortgenai

import (
	"context"
	"errors"
)

var ErrNotInitialized = errors.New("ORT GenAI is not supported on this platform")

func IsInitialized() bool { return false }

func InitializeEnvironment() error {
	return errors.New("ORT GenAI is not supported on this platform")
}

func DestroyEnvironment() error { return nil }

func SetSharedLibraryPath(path string) {}

type GenerationOptions struct {
	MaxLength int
	BatchSize int
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Session struct{}

type SequenceDelta struct {
	Sequence int
	Tokens   string
}

type Statistics struct {
	AvgPrefillSeconds float64
	TokensPerSecond   float64
}

func (s *Session) GetStatistics() Statistics { return Statistics{} }

func (s *Session) Generate(_ context.Context, _ [][]Message, _ *GenerationOptions) (<-chan SequenceDelta, <-chan error, error) {
	return nil, nil, errors.New("ORT GenAI is not supported on this platform")
}

func (s *Session) Destroy() {}

func CreateGenerativeSessionAdvanced(_ string, _ []string, _ map[string]map[string]string) (*Session, error) {
	return nil, errors.New("ORT GenAI is not supported on this platform")
}

func CreateGenerativeSession(_ string) (*Session, error) {
	return nil, errors.New("ORT GenAI is not supported on this platform")
}
