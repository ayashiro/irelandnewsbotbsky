#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   PROJECT_ID="your-gcp-project-id" \
#   BLUESKY_HANDLE="yourhandle.bsky.social" \
#   ./deploy/setup-gcp.sh
#
# Optional env vars:
#   REGION=europe-west1        (default: europe-west1)
#   JOB_NAME=ireland-news-bot  (default: ireland-news-bot)
#   GEMINI_MODEL=gemini-4-hydrogen

: "${PROJECT_ID:?Set PROJECT_ID (e.g. PROJECT_ID=my-gcp-project)}"
: "${BLUESKY_HANDLE:?Set BLUESKY_HANDLE (e.g. BLUESKY_HANDLE=mybot.bsky.social)}"

REGION="${REGION:-europe-west1}"
JOB_NAME="${JOB_NAME:-ireland-news-bot}"
GEMINI_MODEL="${GEMINI_MODEL:-gemini-4-hydrogen}"
JOB_SA_NAME="${JOB_NAME}-job"
SCHEDULER_SA_NAME="${JOB_NAME}-scheduler"
SCHEDULER_NAME="${JOB_NAME}-daily-8am"

JOB_SA_EMAIL="${JOB_SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
SCHEDULER_SA_EMAIL="${SCHEDULER_SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

echo "==> Setting active project: ${PROJECT_ID}"
gcloud config set project "${PROJECT_ID}"

echo "==> Enabling required GCP APIs..."
gcloud services enable \
  run.googleapis.com \
  cloudscheduler.googleapis.com \
  secretmanager.googleapis.com \
  cloudbuild.googleapis.com \
  artifactregistry.googleapis.com

echo "==> Creating service accounts (if not exists)..."
gcloud iam service-accounts describe "${JOB_SA_EMAIL}" >/dev/null 2>&1 || \
  gcloud iam service-accounts create "${JOB_SA_NAME}" --display-name="Ireland News Bot Cloud Run Job"

gcloud iam service-accounts describe "${SCHEDULER_SA_EMAIL}" >/dev/null 2>&1 || \
  gcloud iam service-accounts create "${SCHEDULER_SA_NAME}" --display-name="Ireland News Bot Scheduler"

ensure_secret() {
  local name="$1"
  local prompt="$2"
  if ! gcloud secrets describe "${name}" >/dev/null 2>&1; then
    echo "==> Creating secret: ${name}"
    gcloud secrets create "${name}" --replication-policy="automatic"
    read -rsp "${prompt}: " secret_val
    echo ""
    printf "%s" "${secret_val}" | gcloud secrets versions add "${name}" --data-file=-
  else
    echo "==> Secret ${name} already exists (skipping creation)"
  fi

  gcloud secrets add-iam-policy-binding "${name}" \
    --member="serviceAccount:${JOB_SA_EMAIL}" \
    --role="roles/secretmanager.secretAccessor" >/dev/null
}

ensure_secret "GEMINI_API_KEY" "Enter GEMINI_API_KEY"
ensure_secret "BLUESKY_APP_PASSWORD" "Enter BLUESKY_APP_PASSWORD (xxxx-xxxx-xxxx-xxxx)"

echo "==> Building and deploying Cloud Run Job: ${JOB_NAME} (${REGION})..."
gcloud run jobs deploy "${JOB_NAME}" \
  --source . \
  --region "${REGION}" \
  --service-account "${JOB_SA_EMAIL}" \
  --max-retries 2 \
  --task-timeout 180s \
  --memory 512Mi \
  --cpu 1 \
  --set-env-vars "BLUESKY_HANDLE=${BLUESKY_HANDLE},GEMINI_MODEL=${GEMINI_MODEL},TIMEZONE=Europe/Dublin" \
  --set-secrets "GEMINI_API_KEY=GEMINI_API_KEY:latest,BLUESKY_APP_PASSWORD=BLUESKY_APP_PASSWORD:latest"

echo "==> Granting Cloud Run Invoker role to Scheduler service account..."
gcloud run jobs add-iam-policy-binding "${JOB_NAME}" \
  --region "${REGION}" \
  --member="serviceAccount:${SCHEDULER_SA_EMAIL}" \
  --role="roles/run.invoker" >/dev/null

JOB_RUN_URI="https://${REGION}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${PROJECT_ID}/jobs/${JOB_NAME}:run"

echo "==> Creating/updating Cloud Scheduler job (08:00 Europe/Dublin daily)..."
if gcloud scheduler jobs describe "${SCHEDULER_NAME}" --location="${REGION}" >/dev/null 2>&1; then
  gcloud scheduler jobs update http "${SCHEDULER_NAME}" \
    --location="${REGION}" \
    --schedule="0 8 * * *" \
    --time-zone="Europe/Dublin" \
    --uri="${JOB_RUN_URI}" \
    --http-method=POST \
    --oauth-service-account-email="${SCHEDULER_SA_EMAIL}"
else
  gcloud scheduler jobs create http "${SCHEDULER_NAME}" \
    --location="${REGION}" \
    --schedule="0 8 * * *" \
    --time-zone="Europe/Dublin" \
    --uri="${JOB_RUN_URI}" \
    --http-method=POST \
    --oauth-service-account-email="${SCHEDULER_SA_EMAIL}"
fi

echo ""
echo "Done! To test immediately on Cloud Run:"
echo "  gcloud run jobs execute ${JOB_NAME} --region ${REGION} --wait"
