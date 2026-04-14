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

data "google_project" "main" {}

# ランダムサフィックスで冪等性を確保
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  suffix = random_id.suffix.hex
}

# ──────────────────────────────────────────────
# VPC Network
# ──────────────────────────────────────────────

resource "google_compute_network" "main" {
  name                    = "cb-pool-${local.suffix}"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "main" {
  name                     = "cb-pool-subnet-${local.suffix}"
  network                  = google_compute_network.main.id
  ip_cidr_range            = "10.0.0.0/24"
  region                   = var.region
  private_ip_google_access = true
}

# ──────────────────────────────────────────────
# Cloud Router + Cloud NAT（Worker の外部通信用）
# ──────────────────────────────────────────────

resource "google_compute_router" "main" {
  name    = "cb-router-${local.suffix}"
  network = google_compute_network.main.id
  region  = var.region
}

resource "google_compute_router_nat" "main" {
  name                               = "cb-nat-${local.suffix}"
  router                             = google_compute_router.main.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
}

# ──────────────────────────────────────────────
# Firewall Rules
#
# 方針: deny all egress → 特定の宛先のみ allow
# Worker が VPC 内にいることを FW の挙動で証明する
# ──────────────────────────────────────────────

# すべての外部通信を拒否（最低優先度）
resource "google_compute_firewall" "deny_all_egress" {
  name      = "cb-deny-all-egress-${local.suffix}"
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 65534

  deny {
    protocol = "all"
  }

  destination_ranges = ["0.0.0.0/0"]
}

# Google APIs (restricted.googleapis.com) への通信を許可
resource "google_compute_firewall" "allow_google_apis" {
  name      = "cb-allow-gapis-${local.suffix}"
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["443"]
  }

  destination_ranges = ["199.36.153.4/30"]
}

# 許可テスト用: 特定 IP (dns.google = 8.8.8.8) のみ許可
resource "google_compute_firewall" "allow_test_ip" {
  name      = "cb-allow-test-ip-${local.suffix}"
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["443", "53"]
  }

  allow {
    protocol = "udp"
    ports    = ["53"]
  }

  destination_ranges = ["8.8.8.8/32"]
}

# 内部通信を許可（VPC peering 含む）
resource "google_compute_firewall" "allow_internal" {
  name      = "cb-allow-internal-${local.suffix}"
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "all"
  }

  destination_ranges = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
}

# ──────────────────────────────────────────────
# Service Networking（Private Pool 用 VPC Peering）
# ──────────────────────────────────────────────

resource "google_compute_global_address" "worker_range" {
  name          = "cb-worker-range-${local.suffix}"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 24
  network       = google_compute_network.main.id
}

resource "google_service_networking_connection" "main" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.worker_range.name]
}

# ──────────────────────────────────────────────
# Cloud Build Worker Pool（Private）
# ──────────────────────────────────────────────

resource "google_cloudbuild_worker_pool" "main" {
  name     = "private-pool-${local.suffix}"
  location = var.region

  worker_config {
    disk_size_gb   = 100
    machine_type   = "e2-standard-2"
    no_external_ip = true
  }

  network_config {
    peered_network          = google_compute_network.main.id
    peered_network_ip_range = "/29"
  }

  depends_on = [google_service_networking_connection.main]
}

# ──────────────────────────────────────────────
# Service Accounts
# ──────────────────────────────────────────────

resource "google_service_account" "sa_a" {
  account_id   = "cb-sa-a-${local.suffix}"
  display_name = "Cloud Build Test SA-A"
}

resource "google_service_account" "sa_b" {
  account_id   = "cb-sa-b-${local.suffix}"
  display_name = "Cloud Build Test SA-B"
}

# Cloud Build Service Agent が各 SA を使えるようにする
resource "google_service_account_iam_member" "agent_act_as_sa_a" {
  service_account_id = google_service_account.sa_a.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:service-${data.google_project.main.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
}

resource "google_service_account_iam_member" "agent_act_as_sa_b" {
  service_account_id = google_service_account.sa_b.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:service-${data.google_project.main.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
}

# ログ書き込み権限（ビルド実行に必要）
resource "google_project_iam_member" "sa_a_log_writer" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.sa_a.email}"
}

resource "google_project_iam_member" "sa_b_log_writer" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.sa_b.email}"
}

# ──────────────────────────────────────────────
# GCS Buckets（SA 権限分離の検証用）
# ──────────────────────────────────────────────

resource "google_storage_bucket" "bucket_a" {
  name                        = "cb-test-a-${local.suffix}"
  location                    = var.region
  force_destroy               = true
  uniform_bucket_level_access = true
}

resource "google_storage_bucket" "bucket_b" {
  name                        = "cb-test-b-${local.suffix}"
  location                    = var.region
  force_destroy               = true
  uniform_bucket_level_access = true
}

# SA-A → Bucket-A のみアクセス可
resource "google_storage_bucket_iam_member" "sa_a_bucket_a" {
  bucket = google_storage_bucket.bucket_a.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.sa_a.email}"
}

# SA-B → Bucket-B のみアクセス可
resource "google_storage_bucket_iam_member" "sa_b_bucket_b" {
  bucket = google_storage_bucket.bucket_b.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.sa_b.email}"
}

# ──────────────────────────────────────────────
# Artifact Registry（VPC 内部接続の検証用）
# ──────────────────────────────────────────────

resource "google_artifact_registry_repository" "main" {
  location      = var.region
  repository_id = "cb-test-${local.suffix}"
  format        = "DOCKER"
}

# SA-A に AR 読み取り権限を付与（接続検証用）
resource "google_artifact_registry_repository_iam_member" "sa_a_reader" {
  location   = google_artifact_registry_repository.main.location
  repository = google_artifact_registry_repository.main.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.sa_a.email}"
}
