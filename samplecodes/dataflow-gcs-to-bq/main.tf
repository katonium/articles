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
  # 5 本の静的な入力 (CSV / TSV / JSON / gzip CSV) + インライン Python の代表 csv_python。
  # さらに csv_dated は「ファイル名と BQ テーブル名の両方に日付を埋め込むパターン」
  # の検証用で、Jinja 条件分岐で (A) 明示パラメータ / (B) 自動計算 の両モードに対応する。
  #
  # csv_dated の BQ 宛先テーブルは日付付きで名前が毎回変わるため、Terraform では作らず
  # Go test が test 実行時に create / drop する。Terraform が管理するのは YAML の
  # GCS upload だけ。
  yaml_files = {
    csv               = "${path.module}/pipelines/csv.yaml"
    tsv               = "${path.module}/pipelines/tsv.yaml"
    json              = "${path.module}/pipelines/json.yaml"
    csv_gz            = "${path.module}/pipelines/csv_gz.yaml"
    csv_python        = "${path.module}/pipelines/csv_python.yaml"
    csv_dated         = "${path.module}/pipelines/csv_dated.yaml"
    csv_required_fail = "${path.module}/pipelines/csv_required_fail.yaml"
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
# BigQuery dataset
# ──────────────────────────────────────────────
#
# 宛先テーブル本体 (静的 15 + dated 3 = 18 個) は Go test が create / drop する。
# 名前が per-run で変わる dated テーブルと静的 15 テーブルとを同じ責務分担で扱うことで、
# 「Terraform = 長生き (バケット / dataset / SA / IAM / YAML)」「Go = 一過性
# (入力データ / テーブル / ジョブ / アサーション)」という線が綺麗に引ける。
#
# dataset は SA の IAM binding が必要なので Terraform 管轄で残す
# (delete_contents_on_destroy で中身ごと片付く)。

resource "google_bigquery_dataset" "verification" {
  project                    = var.project_id
  dataset_id                 = "dataflow_gcs_to_bq_${local.suffix}"
  location                   = var.bq_location
  delete_contents_on_destroy = true

  depends_on = [google_project_service.services]
}
