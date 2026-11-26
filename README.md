# Ireland News Bluesky Bot (Go + Claude / Gemini + Cloud Run Jobs)

毎日アイルランド現地時間（`Europe/Dublin`、夏時間自動対応）の午前8時に、アイルランド主要メディアの RSS フィードを取得し、Claude（または Gemini）で日本語ヘッドラインに要約して Bluesky へ1投稿でコンパクトに配信するボットです。

## 構成

- **ニュース取得 ([rss.go](internal/rss/rss.go))**: アイルランド在住者の生活に密着した6ソース（Met Éireann 気象警報 / RTÉ Ireland / RTÉ Dublin / BreakingNews.ie Ireland / TheJournal.ie / Dublin Live）の RSS を並列取得・重複排除
- **日本語要約 ([internal/llm/](internal/llm/summarizer.go))**: Claude (`claude-sonnet-5-5`) / Gemini (`gemini-4-hydrogen`) 切り替え対応（`LLM_PROVIDER=claude|gemini`）。Bluesky の 300 grapheme 制限を超えた場合は自動で再生成・短縮
- **プロンプト管理 ([prompts/system.txt](prompts/system.txt), [prompts/user.txt](prompts/user.txt))**: Go の `//go:embed` でバイナリに埋め込み。トーンや選別ルールの調整はこれらのテキストファイルを編集するだけでOK
- **Bluesky 投稿 ([bluesky.go](internal/bluesky/bluesky.go))**: AT Protocol (`createSession` / `createRecord`) で投稿。日本語ハッシュタグの UTF-8 バイトオフセット Facet を自動付与
- **定期実行・CI/CD ([cloudbuild.yaml](cloudbuild.yaml))**: GitHub push で Cloud Build がビルド＆Cloud Run Jobs を自動更新。Cloud Scheduler (`0 8 * * *` / `Europe/Dublin`) で毎日実行、シークレットは Secret Manager で管理

---

## ローカルでのテスト方法

### 1. RSS 取得のみ確認（APIキー不要）

```bash
go run ./cmd/bot --rss-only
```

### 2. 要約までドライラン（Bluesky には投稿しない）

`.env.example` をコピーして `.env` を作成し、`ANTHROPIC_API_KEY`（または Gemini を使う場合は `LLM_PROVIDER=gemini` と `GEMINI_API_KEY`）を設定します。

```bash
cp .env.example .env
# .env を編集して API キーを設定
go run ./cmd/bot --dry-run
```

### 3. 実際に Bluesky へテスト投稿

`.env` に `BLUESKY_HANDLE` と `BLUESKY_APP_PASSWORD` を設定して実行します。

```bash
go run ./cmd/bot
```

---

## GCP (Cloud Run Jobs) での運用

`main` ブランチへ push すると [cloudbuild.yaml](cloudbuild.yaml) により自動でコンテナがビルドされ、`ireland-news-bot`（`europe-west1`）が更新されます。

### 手動実行テスト（Cloud Run 上）

```bash
gcloud run jobs execute ireland-news-bot --region europe-west1 --wait
```
