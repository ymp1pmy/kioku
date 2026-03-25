package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yamd1/kioku/internal/embedding"
	"github.com/yamd1/kioku/internal/storage"
	"github.com/yamd1/kioku/internal/summarizer"
)

type Server struct {
	store      *storage.Store
	embedder   *embedding.Embedder
	summarizer *summarizer.Summarizer
	mcpServer  *server.MCPServer
}

func New(store *storage.Store, embedder *embedding.Embedder) *Server {
	s := &Server{
		store:      store,
		embedder:   embedder,
		summarizer: summarizer.New(embedder),
	}

	mcpSrv := server.NewMCPServer(
		"kioku",
		"0.1.0",
		server.WithToolCapabilities(false),
	)

	mcpSrv.AddTool(toolMemoryAdd(), s.handleMemoryAdd)
	mcpSrv.AddTool(toolMemorySearch(), s.handleMemorySearch)
	mcpSrv.AddTool(toolMemoryRecent(), s.handleMemoryRecent)
	mcpSrv.AddTool(toolMemoryDelete(), s.handleMemoryDelete)

	s.mcpServer = mcpSrv
	return s
}

func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcpServer)
}

// --- tool definitions ---

func toolMemoryAdd() mcp.Tool {
	return mcp.NewTool(
		"memory_add",
		mcp.WithDescription("記憶を保存する。content は必須。source と tags は任意。テキストはチャンク分割して各チャンクを embedding 化する。"),
		mcp.WithString("content", mcp.Required(), mcp.Description("保存するテキスト")),
		mcp.WithString("source", mcp.Description("記憶のソース（例: conversation, note, url）")),
		mcp.WithArray("tags", mcp.Description("タグのリスト")),
	)
}

func toolMemorySearch() mcp.Tool {
	return mcp.NewTool(
		"memory_search",
		mcp.WithDescription("クエリに意味的に近い記憶を検索する。ベクトル類似度・キーワード・新しさで再ランキングする。"),
		mcp.WithString("query", mcp.Required(), mcp.Description("検索クエリ")),
		mcp.WithNumber("n", mcp.Description("返す件数（デフォルト: 5）")),
		mcp.WithNumber("max_chars", mcp.Description("content の最大文字数。超えた分は切り詰める（省略で無制限）")),
	)
}

func toolMemoryRecent() mcp.Tool {
	return mcp.NewTool(
		"memory_recent",
		mcp.WithDescription("最近保存した記憶を取得する。"),
		mcp.WithNumber("n", mcp.Description("返す件数（デフォルト: 10）")),
		mcp.WithString("source", mcp.Description("ソースでフィルタ（省略で全件）")),
		mcp.WithNumber("max_chars", mcp.Description("content の最大文字数。超えた分は切り詰める（省略で無制限）")),
	)
}

func toolMemoryDelete() mcp.Tool {
	return mcp.NewTool(
		"memory_delete",
		mcp.WithDescription("指定 ID の記憶を削除する。"),
		mcp.WithString("id", mcp.Required(), mcp.Description("削除する記憶の ID")),
	)
}

// --- handlers ---

func (s *Server) handleMemoryAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content, ok := req.Params.Arguments["content"].(string)
	if !ok || content == "" {
		return mcp.NewToolResultError("content is required"), nil
	}

	source, _ := req.Params.Arguments["source"].(string)

	var tags []string
	if raw, ok := req.Params.Arguments["tags"].([]interface{}); ok {
		for _, v := range raw {
			if t, ok := v.(string); ok {
				tags = append(tags, t)
			}
		}
	}

	// 要約してから保存・embedding する
	summarized, err := s.summarizer.Summarize(content)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("summarize failed: %v", err)), nil
	}

	mem, err := s.store.Add(summarized, source, tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("store failed: %v", err)), nil
	}

	texts := storage.ChunkText(summarized)
	chunks := make([]storage.MemoryChunk, len(texts))
	for i, t := range texts {
		emb, err := s.embedder.Embed(t)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("embedding failed: %v", err)), nil
		}
		chunks[i] = storage.MemoryChunk{
			ChunkIdx:  i,
			Text:      t,
			Embedding: emb,
		}
	}

	if err := s.store.AddChunks(mem.ID, chunks); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("chunk store failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("保存しました。id: %s（%d チャンク）", mem.ID, len(chunks))), nil
}

func (s *Server) handleMemorySearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, ok := req.Params.Arguments["query"].(string)
	if !ok || query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	n := 5
	if v, ok := req.Params.Arguments["n"].(float64); ok && v > 0 {
		n = int(v)
	}

	maxChars := 0
	if v, ok := req.Params.Arguments["max_chars"].(float64); ok && v > 0 {
		maxChars = int(v)
	}

	emb, err := s.embedder.Embed(query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("embedding failed: %v", err)), nil
	}

	results, err := s.store.Search(emb, query, n)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("該当する記憶が見つかりませんでした。"), nil
	}

	return mcp.NewToolResultText(formatSearchResults(results, maxChars)), nil
}

func (s *Server) handleMemoryRecent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	n := 10
	if v, ok := req.Params.Arguments["n"].(float64); ok && v > 0 {
		n = int(v)
	}

	source, _ := req.Params.Arguments["source"].(string)

	maxChars := 0
	if v, ok := req.Params.Arguments["max_chars"].(float64); ok && v > 0 {
		maxChars = int(v)
	}

	memories, err := s.store.Recent(n, source)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("recent failed: %v", err)), nil
	}

	if len(memories) == 0 {
		return mcp.NewToolResultText("記憶が見つかりませんでした。"), nil
	}

	return mcp.NewToolResultText(formatMemories(memories, maxChars)), nil
}

func (s *Server) handleMemoryDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, ok := req.Params.Arguments["id"].(string)
	if !ok || id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}

	if err := s.store.Delete(id); err != nil {
		if err == sql.ErrNoRows {
			return mcp.NewToolResultError(fmt.Sprintf("id %q が見つかりません", id)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("delete failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("削除しました。id: %s", id)), nil
}

// --- helpers ---

func formatSearchResults(results []*storage.SearchResult, maxChars int) string {
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] id: %s (score: %.3f)\n", i+1, r.Memory.ID, r.Score))
		content := r.Memory.Content
		if maxChars > 0 && len([]rune(content)) > maxChars {
			content = string([]rune(content)[:maxChars]) + "…(省略)"
		}
		sb.WriteString(fmt.Sprintf("    content: %s\n", content))
		if r.Memory.Source != "" {
			sb.WriteString(fmt.Sprintf("    source: %s\n", r.Memory.Source))
		}
		if len(r.Memory.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("    tags: %s\n", strings.Join(r.Memory.Tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("    created_at: %s\n", r.Memory.CreatedAt.Format("2006-01-02 15:04:05")))
		if i < len(results)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func formatMemories(memories []*storage.Memory, maxChars int) string {
	var sb strings.Builder
	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("[%d] id: %s\n", i+1, m.ID))
		content := m.Content
		if maxChars > 0 && len([]rune(content)) > maxChars {
			content = string([]rune(content)[:maxChars]) + "…(省略)"
		}
		sb.WriteString(fmt.Sprintf("    content: %s\n", content))
		if m.Source != "" {
			sb.WriteString(fmt.Sprintf("    source: %s\n", m.Source))
		}
		if len(m.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("    tags: %s\n", strings.Join(m.Tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("    created_at: %s\n", m.CreatedAt.Format("2006-01-02 15:04:05")))
		if i < len(memories)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
