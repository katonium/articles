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

# ランダムサフィックスで冪等性を確保
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  suffix = random_id.suffix.hex
}

# ──────────────────────────────────────────────
# Service Accounts
# ──────────────────────────────────────────────

# ユーザー1: 管理者（dataset_a, dataset_b, dataset_c すべてにアクセス可能）
resource "google_service_account" "user1" {
  account_id   = "bqview-user1-${local.suffix}"
  display_name = "BQ Authorized View Test - User 1 (Admin)"
  project      = var.project_id
}

# ユーザー2: 制限付きユーザー（dataset_b のみアクセス可能）
resource "google_service_account" "user2" {
  account_id   = "bqview-user2-${local.suffix}"
  display_name = "BQ Authorized View Test - User 2 (Restricted)"
  project      = var.project_id
}

# テスト実行者が user1, user2 に impersonate できるようにする
# テスト実行者には roles/iam.serviceAccountTokenCreator が必要（プロジェクトレベルで付与済みの想定）

# ──────────────────────────────────────────────
# Dataset A: 機密データ（元データ）
# ──────────────────────────────────────────────

resource "google_bigquery_dataset" "dataset_a" {
  dataset_id    = "authorized_view_a_${local.suffix}"
  friendly_name = "Dataset A (Confidential)"
  location      = var.location
  project       = var.project_id

  # デフォルトのアクセス制御を削除し、明示的に管理
  delete_contents_on_destroy = true
}

# dataset_a のオーナー権限を user1 に付与
resource "google_bigquery_dataset_iam_member" "dataset_a_owner_user1" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_a.dataset_id
  role       = "roles/bigquery.dataOwner"
  member     = "serviceAccount:${google_service_account.user1.email}"
}

# 機密テーブル
resource "google_bigquery_table" "confidential_data" {
  dataset_id          = google_bigquery_dataset.dataset_a.dataset_id
  table_id            = "confidential_data"
  project             = var.project_id
  deletion_protection = false

  schema = jsonencode([
    { name = "id", type = "INT64", mode = "REQUIRED" },
    { name = "name", type = "STRING", mode = "REQUIRED" },
    { name = "email", type = "STRING", mode = "REQUIRED" },
    { name = "salary", type = "INT64", mode = "REQUIRED" },
  ])
}

# テストデータ投入
resource "google_bigquery_table" "confidential_data_insert" {
  dataset_id          = google_bigquery_dataset.dataset_a.dataset_id
  table_id            = "confidential_data_seed"
  project             = var.project_id
  deletion_protection = false

  view {
    query          = <<-SQL
      SELECT * FROM UNNEST([
        STRUCT<id INT64, name STRING, email STRING, salary INT64>
        (1, 'Alice', 'alice@example.com', 8000000),
        (2, 'Bob', 'bob@example.com', 6000000),
        (3, 'Charlie', 'charlie@example.com', 7000000)
      ])
    SQL
    use_legacy_sql = false
  }
}

# ──────────────────────────────────────────────
# Dataset B: 公開用（承認済みビューを配置）
# ──────────────────────────────────────────────

resource "google_bigquery_dataset" "dataset_b" {
  dataset_id    = "authorized_view_b_${local.suffix}"
  friendly_name = "Dataset B (Public Views)"
  location      = var.location
  project       = var.project_id

  delete_contents_on_destroy = true
}

# dataset_b のオーナー権限を user1 に付与
resource "google_bigquery_dataset_iam_member" "dataset_b_owner_user1" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_b.dataset_id
  role       = "roles/bigquery.dataOwner"
  member     = "serviceAccount:${google_service_account.user1.email}"
}

# dataset_b の閲覧権限を user2 に付与
resource "google_bigquery_dataset_iam_member" "dataset_b_viewer_user2" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_b.dataset_id
  role       = "roles/bigquery.dataViewer"
  member     = "serviceAccount:${google_service_account.user2.email}"
}

# user2 に dataset_b の dataEditor 権限を付与（ビューの作成・更新テスト用）
resource "google_bigquery_dataset_iam_member" "dataset_b_editor_user2" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_b.dataset_id
  role       = "roles/bigquery.dataEditor"
  member     = "serviceAccount:${google_service_account.user2.email}"
}

# 承認済みビュー: name と email のみ公開
resource "google_bigquery_table" "authorized_view" {
  dataset_id          = google_bigquery_dataset.dataset_b.dataset_id
  table_id            = "public_directory"
  project             = var.project_id
  deletion_protection = false

  view {
    query          = "SELECT id, name, email FROM `${var.project_id}.${google_bigquery_dataset.dataset_a.dataset_id}.confidential_data`"
    use_legacy_sql = false
  }
}

# Dataset A に対して Dataset B のビューを承認
resource "google_bigquery_dataset_access" "authorize_view_b" {
  dataset_id = google_bigquery_dataset.dataset_a.dataset_id
  project    = var.project_id

  view {
    project_id = var.project_id
    dataset_id = google_bigquery_dataset.dataset_b.dataset_id
    table_id   = google_bigquery_table.authorized_view.table_id
  }
}

# ──────────────────────────────────────────────
# Dataset C: 最終公開用（ネストされた承認済みビュー）
# ──────────────────────────────────────────────

resource "google_bigquery_dataset" "dataset_c" {
  dataset_id    = "authorized_view_c_${local.suffix}"
  friendly_name = "Dataset C (Nested Views)"
  location      = var.location
  project       = var.project_id

  delete_contents_on_destroy = true
}

# dataset_c のオーナー権限を user1 に付与
resource "google_bigquery_dataset_iam_member" "dataset_c_owner_user1" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_c.dataset_id
  role       = "roles/bigquery.dataOwner"
  member     = "serviceAccount:${google_service_account.user1.email}"
}

# dataset_c の閲覧権限を user2 に付与
resource "google_bigquery_dataset_iam_member" "dataset_c_viewer_user2" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_c.dataset_id
  role       = "roles/bigquery.dataViewer"
  member     = "serviceAccount:${google_service_account.user2.email}"
}

# user2 に dataset_c の dataEditor 権限を付与（ビュー編集テスト用）
resource "google_bigquery_dataset_iam_member" "dataset_c_editor_user2" {
  project    = var.project_id
  dataset_id = google_bigquery_dataset.dataset_c.dataset_id
  role       = "roles/bigquery.dataEditor"
  member     = "serviceAccount:${google_service_account.user2.email}"
}

# ネストされたビュー: dataset_b のビューを参照
resource "google_bigquery_table" "nested_view" {
  dataset_id          = google_bigquery_dataset.dataset_c.dataset_id
  table_id            = "nested_directory"
  project             = var.project_id
  deletion_protection = false

  view {
    query          = "SELECT id, name FROM `${var.project_id}.${google_bigquery_dataset.dataset_b.dataset_id}.${google_bigquery_table.authorized_view.table_id}`"
    use_legacy_sql = false
  }
}

# Dataset B に対して Dataset C のネストされたビューを承認
resource "google_bigquery_dataset_access" "authorize_view_c" {
  dataset_id = google_bigquery_dataset.dataset_b.dataset_id
  project    = var.project_id

  view {
    project_id = var.project_id
    dataset_id = google_bigquery_dataset.dataset_c.dataset_id
    table_id   = google_bigquery_table.nested_view.table_id
  }
}

# user2 に BigQuery Job 実行権限を付与
resource "google_project_iam_member" "user2_job_user" {
  project = var.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.user2.email}"
}

# user1 に BigQuery Job 実行権限を付与
resource "google_project_iam_member" "user1_job_user" {
  project = var.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.user1.email}"
}
