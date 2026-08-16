# ──────────────────────────────────────────────
# Cloud Run Job 実行用 SA × 3 (base / guest_a / guest_b)
# それぞれ「自分のプロジェクト内」のリソース (GCS / BQ) に対する read 権限を持つ。
#
# Cross-perimeter (Case 4/5 の guest_b) では、 host (base) リソースに対する
# IAM はあえて付与しない。 deny の理由を VPC-SC + connector レベルに切り出すため。
# (Case 4: host AR / GCS にアクセス → cross-perimeter で deny されるべき。
#  Case 5: guest_b 内リソースへアクセスするが、cross-perim Shared VPC で
#  connector 経由のネットワークが立たないことで結局到達できない、という観測。)
#
# 一方 Case 2 (guest_a Job が host = base のリソースを読む) では、
# IAM が無いと「IAM 由来の deny」になってしまい本検証の関心 (VPC-SC + Shared VPC)
# とぶれるので、 guest_a SA に base リソースの reader を付ける。
# ──────────────────────────────────────────────

resource "google_service_account" "job_base" {
  project      = data.google_project.base.project_id
  account_id   = "cr-job-base-${var.suffix}"
  display_name = "Cloud Run Job SA (base, VPC-SC verify)"

  depends_on = [google_project_service.enabled]
}

resource "google_service_account" "job_guest_a" {
  project      = data.google_project.guest_a.project_id
  account_id   = "cr-job-ga-${var.suffix}"
  display_name = "Cloud Run Job SA (guest_a, VPC-SC verify)"

  depends_on = [google_project_service.enabled]
}

resource "google_service_account" "job_guest_b" {
  project      = data.google_project.guest_b.project_id
  account_id   = "cr-job-gb-${var.suffix}"
  display_name = "Cloud Run Job SA (guest_b, VPC-SC verify)"

  depends_on = [google_project_service.enabled]
}

# ──────────────────────────────────────────────
# Log Writer (3 SA × 自分のプロジェクト)
# ──────────────────────────────────────────────

resource "google_project_iam_member" "job_base_log_writer" {
  project = data.google_project.base.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.job_base.email}"
}

resource "google_project_iam_member" "job_guest_a_log_writer" {
  project = data.google_project.guest_a.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.job_guest_a.email}"
}

resource "google_project_iam_member" "job_guest_b_log_writer" {
  project = data.google_project.guest_b.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.job_guest_b.email}"
}

# ──────────────────────────────────────────────
# GCS / BigQuery reader 権限 (Case 別)
#
# Case 0 (base Job → base resource):    job_base に base bucket/dataset reader
# Case 2 (guest_a Job → base resource): job_guest_a に base bucket/dataset reader (cross-project, same perim)
# Case 3 (guest_a Job → guest_a resource): job_guest_a に guest_a bucket/dataset reader
# Case 4 (guest_b Job → base resource): IAM は付与しない (cross-perim, deny を VPC-SC レイヤーに任せる)
# Case 5 (guest_b Job → guest_b resource): job_guest_b に guest_b bucket/dataset reader
#   (IAM 上は通せる。 deny は cross-perim Shared VPC + connector ネットワーク到達不能で起こることを期待)
# ──────────────────────────────────────────────

# Case 0: job_base -> base
resource "google_storage_bucket_iam_member" "base_bucket_reader_by_job_base" {
  bucket = google_storage_bucket.base.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.job_base.email}"
}

resource "google_bigquery_dataset_iam_member" "base_dataset_reader_by_job_base" {
  project    = google_bigquery_dataset.base.project
  dataset_id = google_bigquery_dataset.base.dataset_id
  role       = "roles/bigquery.dataViewer"
  member     = "serviceAccount:${google_service_account.job_base.email}"
}

resource "google_project_iam_member" "base_bq_job_user_by_job_base" {
  project = data.google_project.base.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.job_base.email}"
}

# Case 2: job_guest_a -> base (cross-project)
resource "google_storage_bucket_iam_member" "base_bucket_reader_by_job_guest_a" {
  bucket = google_storage_bucket.base.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.job_guest_a.email}"
}

resource "google_bigquery_dataset_iam_member" "base_dataset_reader_by_job_guest_a" {
  project    = google_bigquery_dataset.base.project
  dataset_id = google_bigquery_dataset.base.dataset_id
  role       = "roles/bigquery.dataViewer"
  member     = "serviceAccount:${google_service_account.job_guest_a.email}"
}

resource "google_project_iam_member" "base_bq_job_user_by_job_guest_a" {
  project = data.google_project.base.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.job_guest_a.email}"
}

# Case 3: job_guest_a -> guest_a
resource "google_storage_bucket_iam_member" "ga_bucket_reader_by_job_guest_a" {
  bucket = google_storage_bucket.guest_a.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.job_guest_a.email}"
}

resource "google_bigquery_dataset_iam_member" "ga_dataset_reader_by_job_guest_a" {
  project    = google_bigquery_dataset.guest_a.project
  dataset_id = google_bigquery_dataset.guest_a.dataset_id
  role       = "roles/bigquery.dataViewer"
  member     = "serviceAccount:${google_service_account.job_guest_a.email}"
}

resource "google_project_iam_member" "ga_bq_job_user_by_job_guest_a" {
  project = data.google_project.guest_a.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.job_guest_a.email}"
}

# Case 5: job_guest_b -> guest_b
resource "google_storage_bucket_iam_member" "gb_bucket_reader_by_job_guest_b" {
  bucket = google_storage_bucket.guest_b.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.job_guest_b.email}"
}

resource "google_bigquery_dataset_iam_member" "gb_dataset_reader_by_job_guest_b" {
  project    = google_bigquery_dataset.guest_b.project
  dataset_id = google_bigquery_dataset.guest_b.dataset_id
  role       = "roles/bigquery.dataViewer"
  member     = "serviceAccount:${google_service_account.job_guest_b.email}"
}

resource "google_project_iam_member" "gb_bq_job_user_by_job_guest_b" {
  project = data.google_project.guest_b.project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.job_guest_b.email}"
}
