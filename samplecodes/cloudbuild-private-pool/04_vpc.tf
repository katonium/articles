# ──────────────────────────────────────────────
# VPC / Subnet
#
# - 外部通信を完全遮断（NAT なし）
# - サブネットは Private Google Access 有効
# - Private Pool 用の VPC peering は別ファイル
# ──────────────────────────────────────────────

resource "google_compute_network" "main" {
  name                    = "cb-pp-${local.suffix}"
  auto_create_subnetworks = false
  project                 = google_project.base.project_id

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "main" {
  name                     = "cb-pp-subnet-${local.suffix}"
  project                  = google_project.base.project_id
  region                   = var.region
  network                  = google_compute_network.main.id
  ip_cidr_range            = "10.10.0.0/24"
  private_ip_google_access = true
}

# ──────────────────────────────────────────────
# Firewall
#
# 方針: deny all egress を最低優先度で置き、
#   - VPC 内部通信
#   - restricted.googleapis.com VIP (199.36.153.4/30)
# のみを高優先度で許可する。
# 結果として Worker は Google API 以外の外部に出られない。
# ──────────────────────────────────────────────

resource "google_compute_firewall" "deny_all_egress" {
  name      = "cb-pp-deny-egress-${local.suffix}"
  project   = google_project.base.project_id
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 65534

  deny {
    protocol = "all"
  }

  destination_ranges = ["0.0.0.0/0"]
}

resource "google_compute_firewall" "allow_restricted_googleapis" {
  name      = "cb-pp-allow-rgapi-${local.suffix}"
  project   = google_project.base.project_id
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["443"]
  }

  destination_ranges = ["199.36.153.4/30"]
}

resource "google_compute_firewall" "allow_internal_egress" {
  name      = "cb-pp-allow-internal-${local.suffix}"
  project   = google_project.base.project_id
  network   = google_compute_network.main.id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "all"
  }

  destination_ranges = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
}

# ──────────────────────────────────────────────
# Private DNS
#
# *.googleapis.com / *.pkg.dev を restricted.googleapis.com (199.36.153.4/30) に解決させる。
# これにより VPC-SC 境界内のリクエストとして Google API が使える。
# ──────────────────────────────────────────────

resource "google_dns_managed_zone" "googleapis" {
  name        = "googleapis-${local.suffix}"
  project     = google_project.base.project_id
  dns_name    = "googleapis.com."
  description = "Route googleapis.com to restricted VIP for VPC-SC"
  visibility  = "private"

  private_visibility_config {
    networks {
      network_url = google_compute_network.main.id
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_dns_record_set" "googleapis_restricted_a" {
  project      = google_project.base.project_id
  managed_zone = google_dns_managed_zone.googleapis.name
  name         = "restricted.googleapis.com."
  type         = "A"
  ttl          = 300
  rrdatas      = ["199.36.153.4", "199.36.153.5", "199.36.153.6", "199.36.153.7"]
}

resource "google_dns_record_set" "googleapis_wildcard_cname" {
  project      = google_project.base.project_id
  managed_zone = google_dns_managed_zone.googleapis.name
  name         = "*.googleapis.com."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["restricted.googleapis.com."]
}

resource "google_dns_managed_zone" "pkg_dev" {
  name        = "pkg-dev-${local.suffix}"
  project     = google_project.base.project_id
  dns_name    = "pkg.dev."
  description = "Route pkg.dev to restricted VIP for Artifact Registry"
  visibility  = "private"

  private_visibility_config {
    networks {
      network_url = google_compute_network.main.id
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_dns_record_set" "pkg_dev_a" {
  project      = google_project.base.project_id
  managed_zone = google_dns_managed_zone.pkg_dev.name
  name         = "pkg.dev."
  type         = "A"
  ttl          = 300
  rrdatas      = ["199.36.153.4", "199.36.153.5", "199.36.153.6", "199.36.153.7"]
}

resource "google_dns_record_set" "pkg_dev_wildcard_cname" {
  project      = google_project.base.project_id
  managed_zone = google_dns_managed_zone.pkg_dev.name
  name         = "*.pkg.dev."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["pkg.dev."]
}

# ──────────────────────────────────────────────
# Service Networking (Private Pool 用 VPC Peering)
# ──────────────────────────────────────────────

resource "google_compute_global_address" "worker_range" {
  name          = "cb-pp-worker-${local.suffix}"
  project       = google_project.base.project_id
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 24
  network       = google_compute_network.main.id
}

resource "google_service_networking_connection" "main" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.worker_range.name]

  depends_on = [google_project_service.enabled]
}
