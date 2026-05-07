# ──────────────────────────────────────────────
# Cloud Build Worker Pool (Private)
#
# - no_external_ip = true で Worker から外部到達不可
# - peered_network で本 VPC と Service Networking 経由で接続
# ──────────────────────────────────────────────

resource "google_cloudbuild_worker_pool" "main" {
  name     = "private-pool-${local.suffix}"
  project  = var.project_id
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

  depends_on = [
    google_service_networking_connection.main,
    google_project_service.enabled,
  ]
}
