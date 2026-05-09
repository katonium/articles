# ──────────────────────────────────────────────
# Shared VPC (Stage 2b)
#
# - base を Shared VPC host project にする。
# - guest_a / guest_b を service project として attach する。
# - 共有用に main VPC へ専用 subnet を 1 本足し、
#   guest 側 Cloud Build P4SA / build SA に networkUser を最小付与する。
# - guest pool の peered_network は host (base) の main VPC を full self_link
#   形式で指す (CrossProjectRef)。
# ──────────────────────────────────────────────

resource "google_compute_shared_vpc_host_project" "base" {
  project = google_project.base.project_id

  depends_on = [google_project_service.enabled]
}

resource "google_compute_shared_vpc_service_project" "guest_a" {
  host_project    = google_compute_shared_vpc_host_project.base.project
  service_project = google_project.guest_a.project_id

  depends_on = [google_project_service.enabled]
}

resource "google_compute_shared_vpc_service_project" "guest_b" {
  host_project    = google_compute_shared_vpc_host_project.base.project
  service_project = google_project.guest_b.project_id

  depends_on = [google_project_service.enabled]
}

# ──────────────────────────────────────────────
# 共有用 subnet (main VPC 上に新設)
#
# 既存の cb-pp-subnet-* (10.10.0.0/24) は base 専用のままにし、
# Shared VPC 経由で guest が触る subnet はこちらに分けておく。
# ──────────────────────────────────────────────

resource "google_compute_subnetwork" "shared" {
  name                     = "cb-pp-shared-${local.suffix}"
  project                  = google_project.base.project_id
  region                   = var.region
  network                  = google_compute_network.main.id
  ip_cidr_range            = "10.50.0.0/24"
  private_ip_google_access = true
}

# ──────────────────────────────────────────────
# Subnet IAM: networkUser
#
# guest project の Cloud Build P4SA + build SA に対し、
# 共有 subnet 単位で roles/compute.networkUser を付与する。
# binding にすると他の権限を吹き飛ばすので member を 4 個並べる。
# ──────────────────────────────────────────────

locals {
  shared_subnet_network_users = {
    "guest_a_p4sa"     = "serviceAccount:${local.cb_agent_sa["guest_a"]}"
    "guest_a_build_sa" = "serviceAccount:${google_service_account.build_guest_a.email}"
    "guest_b_p4sa"     = "serviceAccount:${local.cb_agent_sa["guest_b"]}"
    "guest_b_build_sa" = "serviceAccount:${google_service_account.build_guest_b.email}"
  }
}

resource "google_compute_subnetwork_iam_member" "shared_network_user" {
  for_each = local.shared_subnet_network_users

  project    = google_compute_subnetwork.shared.project
  region     = google_compute_subnetwork.shared.region
  subnetwork = google_compute_subnetwork.shared.name
  role       = "roles/compute.networkUser"
  member     = each.value
}
