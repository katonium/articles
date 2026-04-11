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
    "compute.googleapis.com",
    "iam.googleapis.com",
  ])

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

# ──────────────────────────────────────────────
# GCS バケット (input / staging / temp / pipelines)
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
# Beam YAML パイプラインを GCS にアップロード
# ──────────────────────────────────────────────
#
# YAML はリポジトリ内の pipelines/ 配下を Source of Truth にし、
# Terraform でハッシュベースに GCS に upload する。Job Builder GUI で
# 編集 → エクスポートして diff を取りやすくするためにバージョン管理している。

locals {
  # 4 本の SQL パイプラインに加え、インライン Python の代表として csv_python を 1 本。
  # csv_python は入力ファイルは csv.yaml と同じ sample.csv を使い、変換ロジックだけ
  # Python (MapToFields / Filter / PyTransform) で書いている。
  yaml_files = {
    csv        = "${path.module}/pipelines/csv.yaml"
    tsv        = "${path.module}/pipelines/tsv.yaml"
    json       = "${path.module}/pipelines/json.yaml"
    csv_gz     = "${path.module}/pipelines/csv_gz.yaml"
    csv_python = "${path.module}/pipelines/csv_python.yaml"
  }
}

resource "google_storage_bucket_object" "yaml_pipelines" {
  for_each = local.yaml_files

  name   = "pipelines/${each.key}.yaml"
  bucket = google_storage_bucket.workspace.name
  source = each.value

  # ファイル変更時に再アップロードされるよう content_type と detect_md5_hash を使う
  content_type = "application/x-yaml"
}

# ──────────────────────────────────────────────
# Dataflow ワーカー SA + IAM
# ──────────────────────────────────────────────

resource "google_service_account" "dataflow_worker" {
  account_id   = "df-gcs-to-bq-${local.suffix}"
  display_name = "Dataflow GCS->BQ verification worker"
  project      = var.project_id
}

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
# BigQuery dataset + 12 個の宛先テーブル
# ──────────────────────────────────────────────

resource "google_bigquery_dataset" "verification" {
  project                    = var.project_id
  dataset_id                 = "dataflow_gcs_to_bq_${local.suffix}"
  location                   = var.bq_location
  delete_contents_on_destroy = true

  depends_on = [google_project_service.services]
}

locals {
  # 検証 matrix の "形式 × Python 1 本" の 5 本。csv_python は CSV ファイル入力 +
  # インライン Python 変換のケースで、ファイル形式というより「変換言語の比較対象」。
  formats = ["csv", "tsv", "json", "csv_gz", "csv_python"]

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

  # {format}_{pattern} の組み合わせを 12 件展開
  table_specs = merge([
    for fmt in local.formats : {
      for pat, schema in local.patterns :
      "${fmt}_${pat}" => {
        schema = schema
        fmt    = fmt
        pat    = pat
      }
    }
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
