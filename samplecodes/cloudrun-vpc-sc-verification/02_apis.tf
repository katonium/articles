# ──────────────────────────────────────────────
# Cloud Run / VPC Access / Storage / BigQuery API を 3 プロジェクトで有効化。
# cloudbuild-private-pool 側で既に有効化されている API は disable_on_destroy=false
# にしているので二重有効化しても問題ない。
# ──────────────────────────────────────────────

data "google_project" "base" {
  project_id = var.base_project_id
}

data "google_project" "guest_a" {
  project_id = var.guest_a_project_id
}

data "google_project" "guest_b" {
  project_id = var.guest_b_project_id
}

locals {
  all_projects = {
    base    = data.google_project.base
    guest_a = data.google_project.guest_a
    guest_b = data.google_project.guest_b
  }

  required_services = [
    "run.googleapis.com",
    "vpcaccess.googleapis.com",
    "storage.googleapis.com",
    "bigquery.googleapis.com",
    "logging.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
  ]

  all_project_services = {
    for entry in flatten([
      for k, p in local.all_projects : [
        for svc in local.required_services : {
          key     = "${k}--${svc}"
          project = p.project_id
          service = svc
        }
      ]
    ]) : entry.key => entry
  }
}

resource "google_project_service" "enabled" {
  for_each = local.all_project_services

  project            = each.value.project
  service            = each.value.service
  disable_on_destroy = false
}
