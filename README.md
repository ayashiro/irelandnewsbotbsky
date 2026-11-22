# Ireland News Bluesky Bot (Go + Gemini + Cloud Run Jobs)

毎日アイルランド現地時間（`Europe/Dublin`、夏時間自動対応）の午前8時に、アイルランド主要メディアの RSS フィードを取得し、Gemini で日本語ヘッドラインに要約して Bluesky へ1投稿でコンパクトに配信するボットです。

## 構成

- **ニュース取得 ([rss.go](internal/rss/rss.go))**: RTÉ News / TheJournal.ie / The Irish Times の RSS を並列取得・重複排除
- **日本語要約 ([gemini.go](internal/gemini/gemini.go))**: Gemini API (`gemini-4-hydrogen`) で3〜4件のヘッドラインを作成（Bluesky の 300 grapheme 制限を超えた場合は自動で再生成・短縮）
- **Bluesky 投稿 ([bluesky.go](internal/bluesky/bluesky.go))**: AT Protocol (`createSession` / `createRecord`) で投稿。日本語ハッシュタグの UTF-8 バイトオフセット Facet を自動付与
- **定期実行 ([setup-gcp.sh](deploy/setup-gcp.sh))**: GCP Cloud Run Jobs + Cloud Scheduler (`0 8 * * *` / `Europe/Dublin`) + Secret Manager

---

## ローカルでのテスト方法

### 1. RSS 取得のみ確認（APIキー不要）

```bash
go run ./cmd/bot --rss-only
```

### 2. Gemini 要約までドライラン（Bluesky には投稿しない）

`.env.example` をコピーして `.env` を作成し、`GEMINI_API_KEY` を設定します。

```bash
cp .env.example .env
# .env を編集して GEMINI_API_KEY を設定
go run ./cmd/bot --dry-run
```

### 3. 実際に Bluesky へテスト投稿

`.env` に `BLUESKY_HANDLE` と `BLUESKY_APP_PASSWORD` を設定して実行します。

```bash
go run ./cmd/bot
```

---

## GCP (Cloud Run Jobs + Cloud Scheduler) へのデプロイ

`gcloud auth login` 済みの状態で、以下を実行すると API 有効化・Secret Manager 登録・ビルド＆デプロイ・毎朝8時（アイルランド現地時間）のスケジューラー設定まで一括で完了します。

```bash
chmod +x deploy/setup-gcp.sh

PROJECT_ID="your-gcp-project-id" \
BLUESKY_HANDLE="yourhandle.bsky.social" \
./deploy/setup-gcp.sh
```

実行途中に `GEMINI_API_KEY` と `BLUESKY_APP_PASSWORD` の入力を求められます（Secret Manager に安全に保存されます）。

### 手動実行テスト（Cloud Run 上）

```bash
gcloud run jobs execute ireland-news-bot --region europe-west1 --wait
```

### 2回目以降のコード更新デプロイ

```bash
gcloud run jobs deploy ireland-news-bot --source . --region europe-west1
```
