# kioku — セマンティック記憶 MCP サーバー

Claude Code に「記憶」を持たせる MCP サーバー。
hugot（Go内 ONNX推論）でローカル埋め込み生成 → SQLite に保存 → チャンクベースのセマンティック検索＋リランキング。
**Ollama 不要。モデルは初回起動時に HuggingFace から自動DL。**

## アーキテクチャ

```
kioku/
├── cmd/kioku/main.go              # エントリポイント（MCPサーバー / CLIの分岐）
├── internal/
│   ├── config/config.go           # 設定（XDG Base Dir / env var）
│   ├── embedding/embedding.go     # hugot で ONNX 推論（HF 自動DL）
│   ├── storage/storage.go         # SQLite CRUD + チャンク分割 + ベクトル検索 + リランキング
│   └── server/server.go           # MCP サーバー + 4ツール
└── go.mod
```

## 依存

- [mcp-go](https://github.com/mark3labs/mcp-go) — MCP サーバー実装
- [hugot](https://github.com/knights-analytics/hugot) — Go内 ONNX 推論
- [go-sqlite3](https://github.com/mattn/go-sqlite3) — ストレージ（CGO 必要）
- [uuid](https://github.com/google/uuid) — ID 生成

**注意**: `go-sqlite3` は CGO を使うので `gcc` が必要。

## セットアップ

### 1. バイナリを取得

**リリースからダウンロード（推奨）**

[Releases](https://github.com/yamd1/kioku/releases) から環境に合ったアーカイブをダウンロード。

| OS | アーカイブ |
|---|---|
| macOS (Apple Silicon) | `kioku-darwin-arm64.tar.gz` |
| Linux | `kioku-linux-amd64.tar.gz` |
| Windows | `kioku-windows-amd64.zip` |

```bash
# macOS / Linux
tar -xzf kioku-darwin-arm64.tar.gz
mv kioku ~/.local/bin/

# Windows（PowerShell）
Expand-Archive kioku-windows-amd64.zip -DestinationPath .
# 展開した kioku.exe を PATH の通った場所へ移動
```

**ソースからビルド**（Go 1.24+ と gcc が必要）

```bash
cd kioku
go build -o ~/.local/bin/kioku ./cmd/kioku/
```

初回起動時にモデル（約90MB）を `~/.local/share/kioku/models/` に自動DL。

### 2. Claude Code に MCP サーバーとして登録

```bash
claude mcp add --scope user kioku ~/.local/bin/kioku
```

### 3. Stop hook でセッションを自動保存（任意）

セッション終了時に会話をQA形式でkiokuに自動保存するスクリプトを作成する。

**`~/.claude/hooks/save-session-to-kioku.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

KIOKU="${HOME}/.local/bin/kioku"
if [[ ! -x "$KIOKU" ]]; then exit 0; fi

input=$(cat)
session_id=$(echo "$input" | jq -r '.session_id // empty' 2>/dev/null)
if [[ -z "$session_id" ]]; then exit 0; fi

cwd_slug=$(echo "$PWD" | tr '/.' '-')
transcript="${HOME}/.claude/projects/${cwd_slug}/${session_id}.jsonl"
if [[ ! -f "$transcript" ]]; then exit 0; fi

qa=$(python3 - "$transcript" <<'PYEOF'
import sys, json

path = sys.argv[1]
pairs = []
current_q = None

with open(path) as f:
    for line in f:
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        t = obj.get("type")
        if t not in ("user", "assistant"):
            continue
        msg = obj.get("message", {})
        role = msg.get("role", t)
        content = msg.get("content", "")
        if isinstance(content, list):
            parts = [b["text"] for b in content if isinstance(b, dict) and b.get("type") == "text"]
            content = "\n".join(parts)
        content = content.strip()
        if not content:
            continue
        if role == "user":
            current_q = content
        elif role == "assistant" and current_q is not None:
            pairs.append((current_q, content))
            current_q = None

if not pairs:
    sys.exit(1)

lines = []
for q, a in pairs:
    lines.append(f"Q: {q}")
    lines.append(f"A: {a}")
    lines.append("")
print("\n".join(lines).strip())
PYEOF
)

if [[ -z "$qa" ]]; then exit 0; fi

date_tag=$(date +%Y-%m-%d)
echo "$qa" | "$KIOKU" add \
  --source "session:${session_id}" \
  --tags "session,${date_tag},${cwd_slug}" \
  2>/dev/null || true
```

```bash
chmod +x ~/.claude/hooks/save-session-to-kioku.sh
```

`~/.claude/settings.json` に Stop hook を追加:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "bash ~/.claude/hooks/save-session-to-kioku.sh",
            "timeout": 120,
            "statusMessage": "会話をkiokuに保存中...",
            "async": true
          }
        ]
      }
    ]
  }
}
```

## 環境変数

| 変数 | デフォルト | 説明 |
|---|---|---|
| `KIOKU_EMBED_MODEL` | `KnightsAnalytics/all-MiniLM-L6-v2` | HuggingFace のモデル名 |
| `KIOKU_DATA_DIR` | `~/.local/share/kioku` | データ保存先 |

## MCP ツール

| ツール | 必須引数 | 任意引数 | 説明 |
|---|---|---|---|
| `memory_add` | `content` | `source`, `tags` | 記憶を保存。本文をチャンク分割して各チャンクを embedding 化 |
| `memory_search` | `query` | `n` (default: 5), `max_chars` | チャンクベースのセマンティック検索。ベクトル・キーワード・新しさでリランキング |
| `memory_recent` | — | `n` (default: 10), `source`, `max_chars` | 最近の記憶を取得 |
| `memory_delete` | `id` | — | 記憶を削除 |

## CLI サブコマンド

引数なしで起動すると MCP サーバーモード（stdio）。`add` サブコマンドでCLIから記憶を追加できる。

```bash
# stdinからコンテンツを追加
echo "内容" | kioku add --source "memo" --tags "tag1,tag2"

# ファイルから追加
cat conversation.txt | kioku add --source "session:abc123" --tags "session,2026-03-23"
```

| フラグ | デフォルト | 説明 |
|---|---|---|
| `--source` | `cli` | 記憶のソースラベル |
| `--tags` | — | カンマ区切りのタグ |

## 日本語精度を上げたい場合

hugot 対応の多言語モデルに切り替え:

```bash
export KIOKU_EMBED_MODEL=KnightsAnalytics/paraphrase-multilingual-mpnet-base-v2
```
