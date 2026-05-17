# ──────────────────────────────────────────────
# 保護対象リソース (GCS bucket + BigQuery dataset)
#
# - base に 1 セット (Perimeter A 内 / Case 0, C2, C4 で参照)
# - guest_a に 1 セット (Perimeter A 内 / Case 3 で参照)
# - guest_b に 1 セット (Perimeter B 内 / Case 5 で参照)
#
# どれも単純な「存在確認」用なので空 bucket / 空 dataset (1 つの空 table のみ)
# にしておく。 probe スクリプトは `gsutil ls gs://$BUCKET/` と
# `bq show $PROJECT:$DATASET.t` 程度の最小確認に留める。
# ──────────────────────────────────────────────

# base
resource "google_storage_bucket" "base" {
  name                        = "cr-vpcsc-base-${var.suffix}"
  project                     = data.google_project.base.project_id
  location                    = var.region
  force_destroy               = true
  uniform_bucket_level_access = true

  depends_on = [google_project_service.enabled]
}

resource "google_bigquery_dataset" "base" {
  project    = data.google_project.base.project_id
  dataset_id = "cr_vpcsc_base_${var.suffix}"
  location   = var.region

  depends_on = [google_project_service.enabled]
}

resource "google_bigquery_table" "base" {
  project    = google_bigquery_dataset.base.project
  dataset_id = google_bigquery_dataset.base.dataset_id
  table_id   = "t"

  deletion_protection = false

  schema = jsonencode([
    { name = "v", type = "INT64", mode = "NULLABLE" },
  ])
}

# guest_a
resource "google_storage_bucket" "guest_a" {
  name                        = "cr-vpcsc-ga-${var.suffix}"
  project                     = data.google_project.guest_a.project_id
  location                    = var.region
  force_destroy               = true
  uniform_bucket_level_access = true

  depends_on = [google_project_service.enabled]
}

resource "google_bigquery_dataset" "guest_a" {
  project    = data.google_project.guest_a.project_id
  dataset_id = "cr_vpcsc_ga_${var.suffix}"
  location   = var.region

  depends_on = [google_project_service.enabled]
}

resource "google_bigquery_table" "guest_a" {
  project    = google_bigquery_dataset.guest_a.project
  dataset_id = google_bigquery_dataset.guest_a.dataset_id
  table_id   = "t"

  deletion_protection = false

  schema = jsonencode([
    { name = "v", type = "INT64", mode = "NULLABLE" },
  ])
}

# guest_b
resource "google_storage_bucket" "guest_b" {
  name                        = "cr-vpcsc-gb-${var.suffix}"
  project                     = data.google_project.guest_b.project_id
  location                    = var.region
  force_destroy               = true
  uniform_bucket_level_access = true

  depends_on = [google_project_service.enabled]
}

resource "google_bigquery_dataset" "guest_b" {
  project    = data.google_project.guest_b.project_id
  dataset_id = "cr_vpcsc_gb_${var.suffix}"
  location   = var.region

  depends_on = [google_project_service.enabled]
}

resource "google_bigquery_table" "guest_b" {
  project    = google_bigquery_dataset.guest_b.project
  dataset_id = google_bigquery_dataset.guest_b.dataset_id
  table_id   = "t"

  deletion_protection = false

  schema = jsonencode([
    { name = "v", type = "INT64", mode = "NULLABLE" },
  ])
}
