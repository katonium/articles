# ──────────────────────────────────────────────
# Serverless VPC Access Connector × 3
#
# - base / guest_a / guest_b の各プロジェクトに connector を 1 本ずつ作る。
# - base の connector は base 自身が持つ VPC (cb-pp-<suffix>) の自分のサブネットを使う
#   (= "host project に直接置く" 構図。クロスプロジェクト Shared VPC 経由ではない)。
# - guest_a / guest_b の connector は Shared VPC service project から host VPC を
#   subnet 指定で参照する形 (network ではなく subnet を指す)。
#
# 注: VPC Connector は /28 専用サブネット必須。cloudbuild-private-pool の既存
# サブネットは /24 なので再利用できない。本モジュールでこの 3 本の /28 を新設する。
# それぞれ別 CIDR を割り当てて衝突を回避する (10.60.0.0/28, 10.60.0.16/28, 10.60.0.32/28)。
# subnet は host (base) の VPC 上に作り、 guest 側 connector からは subnetwork
# として参照させる。 Shared VPC の networkUser は subnetwork レベルで guest の
# Cloud Run / VPC Access P4SA に付与する必要がある。
# ──────────────────────────────────────────────

# host (base) の既存 VPC を data source で引く (cloudbuild-private-pool が作ったもの)
data "google_compute_network" "host" {
  name    = "cb-pp-${var.suffix}"
  project = data.google_project.base.project_id
}

# ──────────────────────────────────────────────
# VPC Connector 専用サブネット (/28 × 3)
# ──────────────────────────────────────────────

resource "google_compute_subnetwork" "connector_base" {
  name                     = "cr-conn-base-${var.suffix}"
  project                  = data.google_project.base.project_id
  region                   = var.region
  network                  = data.google_compute_network.host.id
  ip_cidr_range            = "10.60.0.0/28"
  private_ip_google_access = true

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "connector_guest_a" {
  name                     = "cr-conn-ga-${var.suffix}"
  project                  = data.google_project.base.project_id
  region                   = var.region
  network                  = data.google_compute_network.host.id
  ip_cidr_range            = "10.60.0.16/28"
  private_ip_google_access = true

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "connector_guest_b" {
  name                     = "cr-conn-gb-${var.suffix}"
  project                  = data.google_project.base.project_id
  region                   = var.region
  network                  = data.google_compute_network.host.id
  ip_cidr_range            = "10.60.0.32/28"
  private_ip_google_access = true

  depends_on = [google_project_service.enabled]
}

# ──────────────────────────────────────────────
# Shared VPC: connector subnet への networkUser 付与
#
# guest_a / guest_b の Cloud Run / VPC Access P4SA + Job 実行 SA に対し、
# それぞれ自分の connector subnet で roles/compute.networkUser を持たせる。
# base 用 subnet は同一プロジェクト内利用なので Shared VPC IAM 不要。
# ──────────────────────────────────────────────

locals {
  vpcaccess_p4sa = {
    guest_a = "serviceAccount:service-${data.google_project.guest_a.number}@gcp-sa-vpcaccess.iam.gserviceaccount.com"
    guest_b = "serviceAccount:service-${data.google_project.guest_b.number}@gcp-sa-vpcaccess.iam.gserviceaccount.com"
  }

  cloudrun_p4sa = {
    guest_a = "serviceAccount:service-${data.google_project.guest_a.number}@serverless-robot-prod.iam.gserviceaccount.com"
    guest_b = "serviceAccount:service-${data.google_project.guest_b.number}@serverless-robot-prod.iam.gserviceaccount.com"
  }
}

resource "google_compute_subnetwork_iam_member" "connector_ga_vpcaccess" {
  project    = google_compute_subnetwork.connector_guest_a.project
  region     = google_compute_subnetwork.connector_guest_a.region
  subnetwork = google_compute_subnetwork.connector_guest_a.name
  role       = "roles/compute.networkUser"
  member     = local.vpcaccess_p4sa["guest_a"]
}

resource "google_compute_subnetwork_iam_member" "connector_ga_cloudrun" {
  project    = google_compute_subnetwork.connector_guest_a.project
  region     = google_compute_subnetwork.connector_guest_a.region
  subnetwork = google_compute_subnetwork.connector_guest_a.name
  role       = "roles/compute.networkUser"
  member     = local.cloudrun_p4sa["guest_a"]
}

resource "google_compute_subnetwork_iam_member" "connector_gb_vpcaccess" {
  project    = google_compute_subnetwork.connector_guest_b.project
  region     = google_compute_subnetwork.connector_guest_b.region
  subnetwork = google_compute_subnetwork.connector_guest_b.name
  role       = "roles/compute.networkUser"
  member     = local.vpcaccess_p4sa["guest_b"]
}

resource "google_compute_subnetwork_iam_member" "connector_gb_cloudrun" {
  project    = google_compute_subnetwork.connector_guest_b.project
  region     = google_compute_subnetwork.connector_guest_b.region
  subnetwork = google_compute_subnetwork.connector_guest_b.name
  role       = "roles/compute.networkUser"
  member     = local.cloudrun_p4sa["guest_b"]
}

# ──────────────────────────────────────────────
# VPC Access Connector (project ごとに 1 本)
# ──────────────────────────────────────────────

resource "google_vpc_access_connector" "base" {
  name    = "cr-conn-base-${var.suffix}"
  project = data.google_project.base.project_id
  region  = var.region

  subnet {
    name       = google_compute_subnetwork.connector_base.name
    project_id = google_compute_subnetwork.connector_base.project
  }

  min_instances = 2
  max_instances = 3
  machine_type  = "e2-micro"

  depends_on = [google_project_service.enabled]
}

resource "google_vpc_access_connector" "guest_a" {
  name    = "cr-conn-ga-${var.suffix}"
  project = data.google_project.guest_a.project_id
  region  = var.region

  subnet {
    name       = google_compute_subnetwork.connector_guest_a.name
    project_id = google_compute_subnetwork.connector_guest_a.project
  }

  min_instances = 2
  max_instances = 3
  machine_type  = "e2-micro"

  depends_on = [
    google_project_service.enabled,
    google_compute_subnetwork_iam_member.connector_ga_vpcaccess,
    google_compute_subnetwork_iam_member.connector_ga_cloudrun,
  ]
}

resource "google_vpc_access_connector" "guest_b" {
  name    = "cr-conn-gb-${var.suffix}"
  project = data.google_project.guest_b.project_id
  region  = var.region

  subnet {
    name       = google_compute_subnetwork.connector_guest_b.name
    project_id = google_compute_subnetwork.connector_guest_b.project
  }

  min_instances = 2
  max_instances = 3
  machine_type  = "e2-micro"

  depends_on = [
    google_project_service.enabled,
    google_compute_subnetwork_iam_member.connector_gb_vpcaccess,
    google_compute_subnetwork_iam_member.connector_gb_cloudrun,
  ]
}
