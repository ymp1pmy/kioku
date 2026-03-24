package embedding

import (
	"fmt"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

type Embedder struct {
	pipeline *pipelines.FeatureExtractionPipeline
	session  *hugot.Session
}

func New(modelsDir, modelName string) (*Embedder, error) {
	session, err := hugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("hugot session: %w", err)
	}

	// モデルが存在しない場合は HuggingFace から自動DL
	modelPath, err := hugot.DownloadModel(modelName, modelsDir, hugot.NewDownloadOptions())
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("download model %q: %w", modelName, err)
	}

	config := hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      "kioku-embedder",
	}
	pipeline, err := hugot.NewPipeline(session, config)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("create pipeline: %w", err)
	}

	return &Embedder{pipeline: pipeline, session: session}, nil
}

func (e *Embedder) Embed(text string) ([]float32, error) {
	results, err := e.pipeline.RunPipeline([]string{text})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(results.Embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return results.Embeddings[0], nil
}

func (e *Embedder) Close() {
	e.session.Destroy()
}
