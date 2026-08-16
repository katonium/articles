# ──────────────────────────────────────────────
# Stage 2a: 追加 Private Pool バリエーション
#
# 04_vpc.tf / 06_cloudbuild.tf のメインライン構成 (peering あり + private DNS +
# restricted VIP ルート + peered_network_ip_range="/29") を Case 0 として、
# ここでは比較対象となる以下 2 ケースの Pool / VPC を base project 内に追加する。
#
# Case 1: peering なし pool (cb-pp-nopeer-<suffix>)
#   - VPC のみ作成し、 google_service_networking_connection を張らない。
#   - private DNS zone も作らない (= *.googleapis.com が restricted VIP に乗らない)。
#   - Pool は network_config を省略 (public-mode) し no_external_ip = true。
#   - 結果: peering 無し + 外部 IP 無し なので、 in-perimeter AR にどの経路でも
#     到達できないことを確認する用途。
#
# Case 2b: peered_network_ip_range 省略 pool (cb-pp-defrange-<suffix>)
#   - main VPC と同じく Service Networking peering + private DNS + FW を作る。
#   - Pool では peered_network_ip_range のみ省略 (デフォルト動作確認)。
#   - 結果: ip_range を明示しなくても peering と疎通できる、を確認する用途。
# ──────────────────────────────────────────────

# ============================================================
# Case 1: peering なし VPC + Pool
# ============================================================

resource "google_compute_network" "nopeer" {
  name                    = "cb-pp-nopeer-${local.suffix}"
  auto_create_subnetworks = false
  project                 = data.google_project.base.project_id

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "nopeer" {
  name                     = "cb-pp-nopeer-subnet-${local.suffix}"
  project                  = data.google_project.base.project_id
  region                   = var.region
  network                  = google_compute_network.nopeer.id
  ip_cidr_range            = "10.20.0.0/24"
  private_ip_google_access = true
}

# FW / DNS は意図的に作らない。
# - Service Networking connection も張らない。
# - そのため Worker VM は peering 経路を持たず、 *.googleapis.com も restricted
#   VIP に解決されない (デフォルト DNS のまま)。
# - Pool は no_external_ip = true なので外部 IP も無し → AR に到達不可。

resource "google_cloudbuild_worker_pool" "nopeer" {
  name     = "private-pool-nopeer-${local.suffix}"
  project  = data.google_project.base.project_id
  location = var.region

  worker_config {
    disk_size_gb   = 100
    machine_type   = "e2-standard-2"
    no_external_ip = true
  }

  # network_config は意図的に省略する。
  # → Pool 自体は public 側の Cloud Build managed network 上で起動するが
  #    no_external_ip=true により外向きインターネットも閉じる。
  # → in-perimeter AR への到達経路が存在しないことの検証になる。

  depends_on = [
    google_project_service.enabled,
  ]
}

# ============================================================
# Case 2b: peered_network_ip_range 省略 pool
# ============================================================

resource "google_compute_network" "defrange" {
  name                    = "cb-pp-defrange-${local.suffix}"
  auto_create_subnetworks = false
  project                 = data.google_project.base.project_id

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "defrange" {
  name                     = "cb-pp-defrange-subnet-${local.suffix}"
  project                  = data.google_project.base.project_id
  region                   = var.region
  network                  = google_compute_network.defrange.id
  ip_cidr_range            = "10.30.0.0/24"
  private_ip_google_access = true
}

# Firewall (main VPC と同じ方針)

resource "google_compute_firewall" "defrange_deny_all_egress" {
  name      = "cb-pp-defrange-deny-egress-${local.suffix}"
  project   = data.google_project.base.project_id
  network   = google_compute_network.defrange.id
  direction = "EGRESS"
  priority  = 65534

  deny {
    protocol = "all"
  }

  destination_ranges = ["0.0.0.0/0"]
}

resource "google_compute_firewall" "defrange_allow_restricted_googleapis" {
  name      = "cb-pp-defrange-allow-rgapi-${local.suffix}"
  project   = data.google_project.base.project_id
  network   = google_compute_network.defrange.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["443"]
  }

  destination_ranges = ["199.36.153.4/30"]
}

resource "google_compute_firewall" "defrange_allow_internal_egress" {
  name      = "cb-pp-defrange-allow-internal-${local.suffix}"
  project   = data.google_project.base.project_id
  network   = google_compute_network.defrange.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "all"
  }

  destination_ranges = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
}

# Private DNS (main VPC と同じ内容を defrange VPC に attach)

resource "google_dns_managed_zone" "defrange_googleapis" {
  name        = "googleapis-defrange-${local.suffix}"
  project     = data.google_project.base.project_id
  dns_name    = "googleapis.com."
  description = "Route googleapis.com to restricted VIP for VPC-SC (defrange)"
  visibility  = "private"

  private_visibility_config {
    networks {
      network_url = google_compute_network.defrange.id
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_dns_record_set" "defrange_googleapis_restricted_a" {
  project      = data.google_project.base.project_id
  managed_zone = google_dns_managed_zone.defrange_googleapis.name
  name         = "restricted.googleapis.com."
  type         = "A"
  ttl          = 300
  rrdatas      = ["199.36.153.4", "199.36.153.5", "199.36.153.6", "199.36.153.7"]
}

resource "google_dns_record_set" "defrange_googleapis_wildcard_cname" {
  project      = data.google_project.base.project_id
  managed_zone = google_dns_managed_zone.defrange_googleapis.name
  name         = "*.googleapis.com."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["restricted.googleapis.com."]
}

resource "google_dns_managed_zone" "defrange_pkg_dev" {
  name        = "pkg-dev-defrange-${local.suffix}"
  project     = data.google_project.base.project_id
  dns_name    = "pkg.dev."
  description = "Route pkg.dev to restricted VIP for Artifact Registry (defrange)"
  visibility  = "private"

  private_visibility_config {
    networks {
      network_url = google_compute_network.defrange.id
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_dns_record_set" "defrange_pkg_dev_a" {
  project      = data.google_project.base.project_id
  managed_zone = google_dns_managed_zone.defrange_pkg_dev.name
  name         = "pkg.dev."
  type         = "A"
  ttl          = 300
  rrdatas      = ["199.36.153.4", "199.36.153.5", "199.36.153.6", "199.36.153.7"]
}

resource "google_dns_record_set" "defrange_pkg_dev_wildcard_cname" {
  project      = data.google_project.base.project_id
  managed_zone = google_dns_managed_zone.defrange_pkg_dev.name
  name         = "*.pkg.dev."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["pkg.dev."]
}

# Service Networking peering for defrange VPC

resource "google_compute_global_address" "defrange_worker_range" {
  name          = "cb-pp-defrange-worker-${local.suffix}"
  project       = data.google_project.base.project_id
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 24
  network       = google_compute_network.defrange.id
}

resource "google_service_networking_connection" "defrange" {
  network                 = google_compute_network.defrange.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.defrange_worker_range.name]

  depends_on = [google_project_service.enabled]
}

resource "google_cloudbuild_worker_pool" "defrange" {
  name     = "private-pool-defrange-${local.suffix}"
  project  = data.google_project.base.project_id
  location = var.region

  worker_config {
    disk_size_gb   = 100
    machine_type   = "e2-standard-2"
    no_external_ip = true
  }

  network_config {
    peered_network = google_compute_network.defrange.id
    # peered_network_ip_range は意図的に省略 (デフォルト動作の確認)
  }

  depends_on = [
    google_service_networking_connection.defrange,
    google_project_service.enabled,
  ]
}
