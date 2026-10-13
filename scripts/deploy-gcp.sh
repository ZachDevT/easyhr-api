#!/usr/bin/env bash
set -euo pipefail

# EasyHR Go API - Google Cloud Run Deployment Script

REGION="us-central1"
SERVICE_NAME="easyhr-api"

# Get current project or fail if unset
PROJECT_ID=$(gcloud config get-value project 2>/dev/null || true)
if [ -z "$PROJECT_ID" ] || [ "$PROJECT_ID" = "(unset)" ]; then
  echo "Error: No active GCP project set. Run: gcloud config set project <PROJECT_ID>"
  exit 1
fi

echo "Deploying EasyHR API to Google Cloud Run..."
echo "Project: $PROJECT_ID"
echo "Region:  $REGION"
echo "Service: $SERVICE_NAME"

IMAGE_NAME="gcr.io/${PROJECT_ID}/${SERVICE_NAME}:latest"

# 1. Build and push container using Google Cloud Build
echo "Building container on Google Cloud Build..."
gcloud builds submit --tag "${IMAGE_NAME}" .

# 2. Deploy to Cloud Run
echo "Deploying container to Cloud Run..."
gcloud run deploy "${SERVICE_NAME}" \
  --image "${IMAGE_NAME}" \
  --region "${REGION}" \
  --platform managed \
  --allow-unauthenticated \
  --memory 512Mi \
  --cpu 1 \
  --min-instances 0 \
  --max-instances 10

echo "Deployment complete! Retrieving service URL..."
gcloud run services describe "${SERVICE_NAME}" --region "${REGION}" --format="value(status.url)"
