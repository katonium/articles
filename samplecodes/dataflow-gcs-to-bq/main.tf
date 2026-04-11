terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# ──────────────────────────────────────────────
# 共通 / 冪等性
# ──────────────────────────────────────────────

resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  suffix = random_id.suffix.hex
}

data "google_client_openid_userinfo" "me" {}

# ──────────────────────────────────────────────
# API 有効化
# ──────────────────────────────────────────────

resource "google_project_service" "services" {
  for_each = toset([
    "dataflow.googleapis.com",
    "bigquery.googleapis.com",
    "storage.googleapis.com",
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "compute.googleapis.com",
    "iam.googleapis.com",
  ])

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

# ──────────────────────────────────────────────
# GCS バケット (input / staging / temp / templates)
# ──────────────────────────────────────────────

resource "google_storage_bucket" "workspace" {
  name                        = "df-gcs-to-bq-${local.suffix}-${var.project_id}"
  project                     = var.project_id
  location                    = var.region
  uniform_bucket_level_access = true
  force_destroy               = true

  depends_on = [google_project_service.services]
}

# ──────────────────────────────────────────────
# Artifact Registry (Flex Template Docker イメージ用)
# ──────────────────────────────────────────────

resource "google_artifact_registry_repository" "flex_template" {
  project       = var.project_id
  location      = var.region
  repository_id = "dataflow-flex-templates-${local.suffix}"
  format        = "DOCKER"
  description   = "Dataflow GCS->BQ verification Flex Template images"

  depends_on = [google_project_service.services]
}

# ──────────────────────────────────────────────
# Dataflow ワーカー SA + IAM
# ──────────────────────────────────────────────

resource "google_service_account" "dataflow_worker" {
  account_id   = "df-gcs-to-bq-${local.suffix}"
  display_name = "Dataflow GCS->BQ verification worker"
  project      = var.project_id
}

# Dataflow ワーカーとして動作するための最小権限
resource "google_project_iam_member" "worker_dataflow" {
  project = var.project_id
  role    = "roles/dataflow.worker"
  member  = "serviceAccount:${google_service_account.dataflow_worker.email}"
}

resource "google_storage_bucket_iam_member" "worker_bucket" {
  bucket = google_storage_bucket.workspace.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.dataflow_worker.email}"
}

resource "google_project_iam_member" "worker_bq_data_editor" {
  project = var.project_id
  role    = "roles/bigquery.dataEditor"
  member  = "serviceAccount:${google_service_account.dataflow_worker.email}"
}

resource "google_project_iam_member" "worker_bq_job_user" {
  project = var.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.dataflow_worker.email}"
}

# テスト実行ユーザがこの SA を Dataflow ジョブのワーカー SA として指定できるようにする
resource "google_service_account_iam_member" "runner_can_actas_worker" {
  service_account_id = google_service_account.dataflow_worker.name
  role               = "roles/iam.serviceAccountUser"
  member             = "user:${data.google_client_openid_userinfo.me.email}"
}

# ──────────────────────────────────────────────
# BigQuery dataset + 24 個の宛先テーブル
# ──────────────────────────────────────────────

resource "google_bigquery_dataset" "verification" {
  project                    = var.project_id
  dataset_id                 = "dataflow_gcs_to_bq_${local.suffix}"
  location                   = var.bq_location
  delete_contents_on_destroy = true

  depends_on = [google_project_service.services]
}

locals {
  formats = ["csv", "tsv", "json", "csv_gz"]
  langs   = ["sql", "py"]

  schema_drop_col = jsonencode([
    { name = "id", type = "INT64", mode = "REQUIRED" },
    { name = "name", type = "STRING", mode = "NULLABLE" },
    { name = "event_at_utc", type = "TIMESTAMP", mode = "REQUIRED" },
  ])

  schema_utc_jst = jsonencode([
    { name = "id", type = "INT64", mode = "REQUIRED" },
    { name = "name", type = "STRING", mode = "NULLABLE" },
    { name = "secret", type = "STRING", mode = "REQUIRED" },
    { name = "event_at_jst", type = "TIMESTAMP", mode = "REQUIRED" },
  ])

  schema_null_drop = jsonencode([
    { name = "id", type = "INT64", mode = "REQUIRED" },
    { name = "name", type = "STRING", mode = "REQUIRED" },
    { name = "secret", type = "STRING", mode = "REQUIRED" },
    { name = "event_at_utc", type = "TIMESTAMP", mode = "REQUIRED" },
  ])

  patterns = {
    drop_col  = local.schema_drop_col
    utc_jst   = local.schema_utc_jst
    null_drop = local.schema_null_drop
  }

  # {format}_{lang}_{pattern} の組み合わせを 24 件分展開
  table_specs = merge([
    for fmt in local.formats : merge([
      for lang in local.langs : {
        for pat, schema in local.patterns :
        "${fmt}_${lang}_${pat}" => {
          schema = schema
          fmt    = fmt
          lang   = lang
          pat    = pat
        }
      }
    ]...)
  ]...)
}

resource "google_bigquery_table" "destinations" {
  for_each = local.table_specs

  project             = var.project_id
  dataset_id          = google_bigquery_dataset.verification.dataset_id
  table_id            = each.key
  deletion_protection = false
  schema              = each.value.schema
}

# ──────────────────────────────────────────────
# Flex Template ビルド (Docker イメージ + spec JSON)
# ──────────────────────────────────────────────
#
# Terraform の責務として「Dataflow パイプライン (= Flex Template)」を
# 作成するため、null_resource で gcloud を local-exec する。
# pipeline/ 配下のソースに変化があったら自動で再ビルドされる。

locals {
  pipeline_dir = "${path.module}/pipeline"
  image_uri = format(
    "%s-docker.pkg.dev/%s/%s/dataflow-gcs-to-bq:%s",
    var.region,
    var.project_id,
    google_artifact_registry_repository.flex_template.repository_id,
    local.suffix,
  )
  template_spec_gcs_path = "gs://${google_storage_bucket.workspace.name}/templates/spec.json"

  # ソースファイルのハッシュを trigger に使う
  pipeline_source_hash = sha256(join("", [
    filesha256("${local.pipeline_dir}/Dockerfile"),
    filesha256("${local.pipeline_dir}/pipeline.py"),
    filesha256("${local.pipeline_dir}/transforms.py"),
    filesha256("${local.pipeline_dir}/requirements.txt"),
    filesha256("${local.pipeline_dir}/metadata.json"),
  ]))
}

resource "null_resource" "flex_template_build" {
  triggers = {
    source_hash = local.pipeline_source_hash
    image_uri   = local.image_uri
    spec_path   = local.template_spec_gcs_path
  }

  provisioner "local-exec" {
    working_dir = local.pipeline_dir
    command     = <<-EOT
      set -euo pipefail

      echo ">>> Building Docker image via Cloud Build: ${local.image_uri}"
      gcloud builds submit \
        --project=${var.project_id} \
        --tag=${local.image_uri} \
        .

      echo ">>> Building Flex Template spec: ${local.template_spec_gcs_path}"
      gcloud dataflow flex-template build ${local.template_spec_gcs_path} \
        --project=${var.project_id} \
        --image=${local.image_uri} \
        --sdk-language=PYTHON \
        --metadata-file=metadata.json
    EOT
  }

  depends_on = [
    google_artifact_registry_repository.flex_template,
    google_storage_bucket.workspace,
    google_project_service.services,
  ]
}
