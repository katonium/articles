# ──────────────────────────────────────────────
# 検証用に追加で作成するプロジェクト
#
# - base: Cloud Build private pool 本体 + Shared VPC host を兼ねる。 Case 0/1/2 の
#   メインライン、 および Case 3/4 における Shared VPC host project でもある。
# - guest_a: Perimeter A 配下の Shared VPC service project (Case 3 検証用)。
# - guest_b: Perimeter B 配下の Shared VPC service project (Case 4 境界分離検証用)。
#
# project_id は global unique なので suffix で衝突回避。 billing は tfvars 経由。
# ──────────────────────────────────────────────

resource "google_project" "base" {
  name                = "VPC-SC CB base shared VPC host"
  project_id          = "cbvpcsc-host-${local.suffix}"
  folder_id           = var.folder_id
  billing_account     = var.billing_account
  auto_create_network = false
  deletion_policy     = "DELETE"
}

resource "google_project" "guest_a" {
  name                = "VPC-SC CB guest A perim A"
  project_id          = "cbvpcsc-guesta2-${local.suffix}"
  folder_id           = var.folder_id
  billing_account     = var.billing_account
  auto_create_network = false
  deletion_policy     = "DELETE"
}

resource "google_project" "guest_b" {
  name                = "VPC-SC CB guest B perim B"
  project_id          = "cbvpcsc-guest-b-${local.suffix}"
  folder_id           = var.folder_id
  billing_account     = var.billing_account
  auto_create_network = false
  deletion_policy     = "DELETE"
}

locals {
  # 各 project で有効化する API。VPC-SC 境界の作成・配下リソースの構築・
  # 後段テストで必要となるもの一式。
  required_services = [
    "accesscontextmanager.googleapis.com",
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "compute.googleapis.com",
    "dns.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "logging.googleapis.com",
    "servicenetworking.googleapis.com",
    "serviceusage.googleapis.com",
    "storage.googleapis.com",
  ]

  all_projects = {
    base    = google_project.base
    guest_a = google_project.guest_a
    guest_b = google_project.guest_b
  }

  # 各 project × 必要 API の組
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

  # Cloud Build Service Agent (P4SA) is per project
  cb_agent_sa = {
    for k, p in local.all_projects :
    k => "service-${p.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
  }
}

resource "google_project_service" "enabled" {
  for_each = local.all_project_services

  project            = each.value.project
  service            = each.value.service
  disable_on_destroy = false
}
